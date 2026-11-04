package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Calcium-Ion/moejs/compiler"
	"github.com/Calcium-Ion/moejs/syntax"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

type PresetScriptIssue struct {
	Code   string `json:"code"`
	Module string `json:"module,omitempty"`
}

type PreparedPresetScript struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Enabled bool                `json:"enabled"`
	Status  string              `json:"status"`
	Issues  []PresetScriptIssue `json:"issues"`
}

type PresetScriptPreparation struct {
	Scripts []PreparedPresetScript `json:"scripts"`
	Modules map[string]string      `json:"modules"`
}

type presetModuleFetch func(context.Context, string) (source, finalURL string, err error)

var presetPreparationSlots = make(chan struct{}, 2)

// PreparePresetScriptDependencies prepares channel-owned snapshots, never runs
// third-party code and never mutates the supplied preset. It is not a compatibility
// proof: browser APIs and computed imports can still require runtime capabilities.
func PreparePresetScriptDependencies(ctx context.Context, config *dto.SillyTavernPresetConfig) (*PresetScriptPreparation, error) {
	select {
	case presetPreparationSlots <- struct{}{}:
		defer func() { <-presetPreparationSlots }()
	default:
		return nil, fmt.Errorf("script preparation is busy")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := newPresetModuleClient(nil, nil)
	defer client.CloseIdleConnections()
	fetch := func(ctx context.Context, name string) (string, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, name, nil)
		if err != nil {
			return "", "", errors.New("invalid URL")
		}
		req.Header.Set("Accept", "text/javascript, application/javascript, text/plain")
		response, err := client.Do(req)
		if err != nil {
			return "", "", errors.New("dependency download failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return "", "", errors.New("dependency server did not return HTTP 200")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, maxPresetScriptSource+1))
		if err != nil || len(data) > maxPresetScriptSource {
			return "", "", errors.New("dependency download exceeded size limit")
		}
		return string(data), response.Request.URL.String(), nil
	}
	return preparePresetDependencyGraph(ctx, config, fetch)
}

func tavernScriptSource(content string) string {
	trimmed := strings.TrimSpace(content)
	if u, err := url.Parse(trimmed); err == nil && u.Scheme == "https" && u.Host != "" && !strings.ContainsAny(trimmed, "\r\n\t ") {
		quoted, _ := common.Marshal(trimmed)
		return "import " + string(quoted) + ";"
	}
	return content
}

func validatePresetModuleURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" ||
		u.User != nil || u.Fragment != "" || strings.Contains(u.Hostname(), "%") ||
		(u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("dependency URL must use public HTTPS on port 443 without credentials or fragments")
	}
	protection := common.SSRFProtection{AllowedPorts: []int{443}}
	if err := protection.ValidateNetworkTarget(u.Hostname(), 443); err != nil {
		return nil, errors.New("dependency destination is not public")
	}
	return u, nil
}

func newPresetModuleClient(resolver ssrfResolver, dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	guard := &protectedFetchDialer{
		resolver: resolver, dialContext: dial,
		getProtection: func() (*common.SSRFProtection, bool, error) {
			return &common.SSRFProtection{AllowedPorts: []int{443}, ApplyIPFilterForDomain: true}, true, nil
		},
	}
	// Deliberately independent of provider proxies and insecure-TLS settings.
	transport := &http.Transport{
		DialContext: guard.DialContext, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10,
		DisableKeepAlives: true,
	}
	return &http.Client{
		Transport: transport, Timeout: 8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many dependency redirects")
			}
			_, err := validatePresetModuleURL(req.URL.String())
			return err
		},
	}
}

type presetDependencyGraph struct {
	ctx     context.Context
	fetch   presetModuleFetch
	modules map[string]string
	visited map[string][]PresetScriptIssue
	edges   map[string][]string
	total   int
}

func preparePresetDependencyGraph(ctx context.Context, config *dto.SillyTavernPresetConfig, fetch presetModuleFetch) (*PresetScriptPreparation, error) {
	if config == nil || len(config.Preset) == 0 || len(config.Preset) > dto.MaxSillyTavernPresetBytes {
		return nil, errors.New("invalid preset for script preparation")
	}
	var preset struct {
		Extensions struct {
			Helper struct {
				Scripts []presetTavernScript `json:"scripts"`
			} `json:"tavern_helper"`
		} `json:"extensions"`
	}
	if err := common.Unmarshal(config.Preset, &preset); err != nil {
		return nil, errors.New("invalid preset script tree")
	}
	graph := &presetDependencyGraph{ctx: ctx, fetch: fetch, modules: map[string]string{}, visited: map[string][]PresetScriptIssue{}, edges: map[string][]string{}}
	for name, source := range config.ScriptModules {
		graph.total += len(source)
		if name == "" || strings.HasPrefix(name, "preset:") || len(name) > 2048 ||
			len(source) > maxPresetScriptSource || graph.total > 1<<20 || len(config.ScriptModules) > 64 {
			return nil, errors.New("invalid or oversized script dependency snapshots")
		}
		graph.modules[name] = source
	}
	result := &PresetScriptPreparation{Scripts: []PreparedPresetScript{}, Modules: graph.modules}
	seen := map[string]bool{}
	nodes, enabledCount := 0, 0
	var visit func([]presetTavernScript, bool, int) error
	visit = func(scripts []presetTavernScript, parentEnabled bool, depth int) error {
		if depth > 8 {
			return errors.New("script folders exceed nesting limit")
		}
		for _, script := range scripts {
			nodes++
			if nodes > 256 {
				return errors.New("too many preset script entries")
			}
			enabled := parentEnabled && script.Enabled
			if script.Type == "folder" {
				if err := visit(script.Scripts, enabled, depth+1); err != nil {
					return err
				}
				continue
			}
			item := PreparedPresetScript{ID: script.ID, Name: script.Name, Enabled: enabled, Status: "disabled", Issues: []PresetScriptIssue{}}
			if enabled {
				enabledCount++
				switch {
				case script.Type != "script":
					item.Issues = append(item.Issues, PresetScriptIssue{Code: "unsupported_entry"})
				case script.ID == "" || len(script.ID) > 128 || seen[script.ID]:
					item.Issues = append(item.Issues, PresetScriptIssue{Code: "invalid_script_id"})
				case enabledCount > 32:
					item.Issues = append(item.Issues, PresetScriptIssue{Code: "script_limit"})
				default:
					source := tavernScriptSource(script.Content)
					graph.total += len(source)
					if graph.total > 1<<20 {
						item.Issues = append(item.Issues, PresetScriptIssue{Code: "source_limit"})
					} else {
						_, item.Issues = graph.inspect("preset:"+script.ID, source, 0)
					}
				}
				seen[script.ID] = true
				item.Status = "prepared"
				for _, issue := range item.Issues {
					if issue.Code == "computed_import" && item.Status != "failed" {
						item.Status = "manual"
					} else if issue.Code != "computed_import" {
						item.Status = "failed"
					}
				}
			}
			result.Scripts = append(result.Scripts, item)
		}
		return nil
	}
	if err := visit(preset.Extensions.Helper.Scripts, true, 0); err != nil {
		return nil, err
	}
	// Resolve each root against the completed graph. A back-edge encountered
	// during a cycle must not permanently hide a later failure in that cycle.
	for i := range result.Scripts {
		item := &result.Scripts[i]
		if !item.Enabled {
			continue
		}
		pending := slices.Clone(graph.edges["preset:"+item.ID])
		visited := map[string]bool{}
		for len(pending) > 0 {
			name := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if visited[name] {
				continue
			}
			visited[name] = true
			for _, issue := range graph.visited[name] {
				if !slices.Contains(item.Issues, issue) && len(item.Issues) < 16 {
					item.Issues = append(item.Issues, issue)
				}
				if issue.Code != "computed_import" {
					item.Status = "failed"
				} else if item.Status == "prepared" {
					item.Status = "manual"
				}
			}
			pending = append(pending, graph.edges[name]...)
		}
	}
	return result, nil
}

// inspect uses AST nodes, not textual import matching, and rewrites only module
// specifier literals. Remote redirects therefore retain browser-relative URL
// semantics when the source is subsequently evaluated from a pinned snapshot.
func (g *presetDependencyGraph) inspect(base, source string, depth int) (string, []PresetScriptIssue) {
	fail := func(code string) (string, []PresetScriptIssue) {
		return source, []PresetScriptIssue{{Code: code, Module: presetModuleDisplay(base)}}
	}
	if len(source) > maxPresetScriptSource {
		return fail("source_limit")
	}
	if g.ctx.Err() != nil {
		return fail("preparation_cancelled")
	}
	tree, err := syntax.ParseModule(base, source, syntax.Options{Stop: g.ctx.Err})
	if err != nil {
		return fail("invalid_javascript")
	}
	if _, err := compiler.CompileModule(tree); err != nil {
		return fail("unsupported_javascript")
	}
	var literals []*syntax.StringLit
	issues := []PresetScriptIssue{}
	syntax.Inspect(tree, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.ImportDecl:
			literals = append(literals, node.Source)
		case *syntax.ExportNamed:
			if node.Source != nil {
				literals = append(literals, node.Source)
			}
		case *syntax.ExportAll:
			literals = append(literals, node.Source)
		case *syntax.ImportCall:
			if literal, ok := node.Source.(*syntax.StringLit); ok {
				literals = append(literals, literal)
			} else {
				issues = append(issues, PresetScriptIssue{Code: "computed_import", Module: presetModuleDisplay(base)})
			}
		}
		return true
	})
	// Reverse source order permits stable literal replacement using AST offsets.
	slices.SortFunc(literals, func(a, b *syntax.StringLit) int { return b.Pos - a.Pos })
	for _, literal := range literals {
		name := literal.Value
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/") {
			baseURL, baseErr := url.Parse(base)
			relative, relativeErr := url.Parse(name)
			if baseErr == nil && relativeErr == nil && baseURL.Scheme == "https" {
				name = baseURL.ResolveReference(relative).String()
				quoted, _ := common.Marshal(name)
				source = source[:literal.Pos] + string(quoted) + source[literal.End:]
			}
		}
		g.edges[base] = append(g.edges[base], name)
		issues = append(issues, g.load(name, depth+1)...)
		if len(issues) >= 16 {
			issues = issues[:16]
			break
		}
	}
	return source, issues
}

func (g *presetDependencyGraph) load(name string, depth int) []PresetScriptIssue {
	if prior, ok := g.visited[name]; ok {
		return prior
	}
	fail := func(code string) []PresetScriptIssue {
		issues := []PresetScriptIssue{{Code: code, Module: presetModuleDisplay(name)}}
		g.visited[name] = issues
		return issues
	}
	if depth > 16 || len(g.visited) >= 64 || len(name) > 2048 {
		return fail("dependency_limit")
	}
	// Mark in progress to terminate legal cyclic module graphs.
	g.visited[name] = nil
	source, pinned := g.modules[name]
	base := name
	if !pinned {
		if _, err := validatePresetModuleURL(name); err != nil {
			return fail("missing_module")
		}
		if g.fetch == nil {
			return fail("download_failed")
		}
		var err error
		source, base, err = g.fetch(g.ctx, name)
		if err != nil {
			return fail("download_failed")
		}
		if _, err := validatePresetModuleURL(base); err != nil {
			return fail("download_failed")
		}
	}
	rewritten, issues := g.inspect(base, source, depth)
	if base != name {
		g.edges[name] = g.edges[base]
	}
	if len(rewritten) > maxPresetScriptSource || g.total+len(rewritten) > 1<<20 && !pinned || len(g.modules) >= 64 && !pinned {
		return fail("source_limit")
	}
	if !pinned {
		g.total += len(rewritten)
		// Retain only parsed source; never replace a good pinned snapshot.
		if !slices.ContainsFunc(issues, func(issue PresetScriptIssue) bool {
			return issue.Code == "invalid_javascript" || issue.Code == "unsupported_javascript"
		}) {
			g.modules[name] = rewritten
		}
	}
	g.visited[name] = issues
	return issues
}

func presetModuleDisplay(name string) string {
	return tavernDiagnosticIdentity(name)
}
