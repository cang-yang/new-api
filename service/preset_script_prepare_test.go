package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparePresetScriptGraph(t *testing.T) {
	config := tavernRuntimeConfig(t, []any{
		map[string]any{"type": "script", "id": "main", "name": "Shared graph", "enabled": true, "content": `import 'https://cdn.example/start.js';`},
		map[string]any{"type": "script", "id": "other", "name": "Other", "enabled": true, "content": `import 'https://cdn.example/start.js';`},
		map[string]any{"type": "folder", "enabled": false, "scripts": []any{
			map[string]any{"type": "script", "id": "off", "name": "Disabled child", "enabled": true, "content": `import 'https://never.example/a.js';`},
		}},
	}, nil)
	calls := map[string]int{}
	fetch := func(_ context.Context, name string) (string, string, error) {
		calls[name]++
		switch name {
		case "https://cdn.example/start.js":
			return `export {x} from './base.js'; const lazy = () => import("./lazy.js"); throw new Error("must not execute");`, "https://cdn.example/v2/start.js", nil
		case "https://cdn.example/v2/base.js":
			return `export const x = 1;`, name, nil
		case "https://cdn.example/v2/lazy.js":
			return `import './base.js'; export default 2;`, name, nil
		}
		return "", "", fmt.Errorf("unexpected fetch")
	}
	result, err := preparePresetDependencyGraph(context.Background(), config, fetch)
	require.NoError(t, err)
	require.Len(t, result.Scripts, 3)
	assert.Equal(t, "prepared", result.Scripts[0].Status)
	assert.Equal(t, "prepared", result.Scripts[1].Status)
	assert.Equal(t, "disabled", result.Scripts[2].Status)
	assert.Equal(t, map[string]int{
		"https://cdn.example/start.js": 1, "https://cdn.example/v2/base.js": 1, "https://cdn.example/v2/lazy.js": 1,
	}, calls)
	assert.Contains(t, result.Modules["https://cdn.example/start.js"], `"https://cdn.example/v2/base.js"`)
	assert.Contains(t, result.Modules["https://cdn.example/start.js"], `"https://cdn.example/v2/lazy.js"`)
	assert.Nil(t, config.ScriptModules, "preparation must not mutate caller config")
	config.ScriptModules = result.Modules
	reused, err := preparePresetDependencyGraph(context.Background(), config, func(context.Context, string) (string, string, error) {
		t.Fatal("pinned snapshots should not be downloaded again")
		return "", "", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "prepared", reused.Scripts[0].Status)
}

func TestPreparePresetScriptFailuresArePerScript(t *testing.T) {
	config := tavernRuntimeConfig(t, []any{
		map[string]any{"type": "script", "id": "computed", "enabled": true, "content": `const path = './x.js'; import(path);`},
		map[string]any{"type": "script", "id": "missing", "enabled": true, "content": `import 'unknown-package';`},
		map[string]any{"type": "script", "id": "broken", "enabled": true, "content": `let = ;`},
		map[string]any{"type": "script", "id": "good", "enabled": true, "content": `const x = "import('not-real')";`},
		map[string]any{"type": "script", "id": "url", "enabled": true, "content": `https://cdn.example/pinned.js`},
	}, map[string]string{"https://cdn.example/pinned.js": "export default 1;"})
	result, err := preparePresetDependencyGraph(context.Background(), config, nil)
	require.NoError(t, err)
	require.Len(t, result.Scripts, 5)
	assert.Equal(t, "manual", result.Scripts[0].Status)
	assert.Equal(t, "computed_import", result.Scripts[0].Issues[0].Code)
	assert.Equal(t, "failed", result.Scripts[1].Status)
	assert.Equal(t, "missing_module", result.Scripts[1].Issues[0].Code)
	assert.Equal(t, "failed", result.Scripts[2].Status)
	assert.Equal(t, "prepared", result.Scripts[3].Status)
	assert.Equal(t, "prepared", result.Scripts[4].Status)
}

func TestPreparePresetScriptBoundsAndURLs(t *testing.T) {
	for _, source := range []string{"", "https://127.0.0.1/a.js", "http://example.com/a.js",
		"https://[::1]/x", "https://user:password@example.com/x", "https://example.com:8443/a",
		"https://169.254.169.254/latest/meta-data", "https://example.com/x#fragment"} {
		t.Run(source, func(t *testing.T) { _, err := validatePresetModuleURL(source); require.Error(t, err) })
	}
	_, err := validatePresetModuleURL("https://cdn.example/x.js?v=1")
	require.NoError(t, err)
	config := tavernRuntimeConfig(t, []any{
		map[string]any{"type": "script", "id": "large", "enabled": true, "content": strings.Repeat(" ", maxPresetScriptSource+1)},
	}, nil)
	result, err := preparePresetDependencyGraph(context.Background(), config, nil)
	require.NoError(t, err)
	assert.Equal(t, "source_limit", result.Scripts[0].Issues[0].Code)
	_, err = preparePresetDependencyGraph(context.Background(), &dto.SillyTavernPresetConfig{}, nil)
	require.Error(t, err)
}

func TestPresetModuleDialRejectsPrivateDNS(t *testing.T) {
	client := newPresetModuleClient(staticSSRFResolver{
		"cdn.example": {{IP: net.ParseIP("127.0.0.1")}},
	}, func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("must not dial a private resolved address")
		return nil, nil
	})
	_, err := client.Get("https://cdn.example/a.js")
	require.Error(t, err)
	redirect, err := http.NewRequest(http.MethodGet, "http://cdn.example/a.js", nil)
	require.NoError(t, err)
	require.Error(t, client.CheckRedirect(redirect, nil))
}

func TestPreparePresetScriptCyclePropagatesFailure(t *testing.T) {
	config := tavernRuntimeConfig(t, []any{
		map[string]any{"type": "script", "id": "a", "enabled": true, "content": `import 'a';`},
		map[string]any{"type": "script", "id": "b", "enabled": true, "content": `import 'b';`},
	}, map[string]string{
		"a": `import 'b'; import 'missing';`,
		"b": `import 'a';`,
	})
	result, err := preparePresetDependencyGraph(context.Background(), config, nil)
	require.NoError(t, err)
	assert.Equal(t, "failed", result.Scripts[0].Status)
	assert.Equal(t, "failed", result.Scripts[1].Status, "shared cyclic graph must not hide missing dependencies")
}

func TestPreparePresetScriptDoesNotLeakMalformedURL(t *testing.T) {
	config := tavernRuntimeConfig(t, []any{
		map[string]any{"type": "script", "id": "bad", "enabled": true, "content": `import 'https://user:%zzSECRET@host/a?key=TOPSECRET';`},
	}, nil)
	result, err := preparePresetDependencyGraph(context.Background(), config, nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.Scripts[0].Issues)
	assert.NotContains(t, result.Scripts[0].Issues[0].Module, "SECRET")
}
