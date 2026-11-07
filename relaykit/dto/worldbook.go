package dto

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
)

const MaxWorldbookBytes = 6 << 20

const (
	WorldbookInjectionFollowGlobal   = "follow_global"
	WorldbookInjectionInherit        = "inherit"
	WorldbookInjectionSystemPrelude  = "system_prelude"
	WorldbookInjectionBeforeLastUser = "before_last_user"
	WorldbookInjectionAtDepth        = "at_depth"
)

type WorldbookConfig struct {
	Enabled        bool        `json:"enabled"`
	ScanDepth      int         `json:"scan_depth"`
	MaxChars       int         `json:"max_chars"`
	Recursive      bool        `json:"recursive"`
	InjectionMode  string      `json:"injection_mode,omitempty"`
	InjectionDepth int         `json:"injection_depth,omitempty"`
	Books          []Worldbook `json:"books"`
}

type Worldbook struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Enabled  bool             `json:"enabled"`
	Entries  []WorldbookEntry `json:"entries"`
	Warnings []string         `json:"warnings,omitempty"`
}

type WorldbookEntry struct {
	ID               string                    `json:"id"`
	Name             string                    `json:"name"`
	Content          string                    `json:"content"`
	Enabled          bool                      `json:"enabled"`
	Constant         bool                      `json:"constant"`
	Keys             []string                  `json:"keys"`
	SecondaryKeys    []string                  `json:"secondary_keys"`
	SecondaryLogic   string                    `json:"secondary_logic"`
	CaseSensitive    bool                      `json:"case_sensitive"`
	WholeWords       bool                      `json:"whole_words"`
	Priority         int                       `json:"priority"`
	InjectionMode    string                    `json:"injection_mode,omitempty"`
	InjectionDepth   int                       `json:"injection_depth,omitempty"`
	PreventRecursion bool                      `json:"prevent_recursion,omitempty"`
	NormalizedRules  []WorldbookNormalizedRule `json:"normalized_rules,omitempty"`
	Issues           []string                  `json:"issues,omitempty"`
	Source           json.RawMessage           `json:"source,omitempty"`
}

type WorldbookNormalizedRule struct {
	Field  string `json:"field"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

type WorldbookPlacement struct {
	Mode     string `json:"mode"`
	Index    int    `json:"index"`
	Role     string `json:"role"`
	Fallback bool   `json:"fallback"`
}

// Limits returns effective defaults without modifying cached channel settings.
func (c *WorldbookConfig) Limits() (int, int) {
	depth, budget := c.ScanDepth, c.MaxChars
	if depth == 0 {
		depth = 4
	}
	if budget == 0 {
		budget = 12000
	}
	return depth, budget
}

func (c *WorldbookConfig) Validate() error {
	if c == nil {
		return nil
	}
	depth, budget := c.Limits()
	if depth < 1 || depth > 50 || budget < 256 || budget > 64000 || len(c.Books) > 16 {
		return fmt.Errorf("worldbook settings exceed supported limits")
	}
	switch c.InjectionMode {
	case "", WorldbookInjectionInherit, WorldbookInjectionSystemPrelude, WorldbookInjectionBeforeLastUser, WorldbookInjectionAtDepth:
	default:
		return fmt.Errorf("invalid worldbook injection mode")
	}
	if c.InjectionDepth < 0 || c.InjectionDepth > 20 {
		return fmt.Errorf("worldbook injection depth must be between 0 and 20")
	}
	count := 0
	for _, book := range c.Books {
		if book.ID == "" || utf8.RuneCountInString(book.ID) > 256 || utf8.RuneCountInString(book.Name) > 256 {
			return fmt.Errorf("worldbook requires a book ID and names of at most 256 characters")
		}
		count += len(book.Entries)
		if count > 1000 {
			return fmt.Errorf("worldbook supports at most 1000 entries")
		}
		for _, entry := range book.Entries {
			switch entry.InjectionMode {
			case "", WorldbookInjectionFollowGlobal, WorldbookInjectionInherit, WorldbookInjectionSystemPrelude, WorldbookInjectionBeforeLastUser, WorldbookInjectionAtDepth:
			default:
				return fmt.Errorf("invalid worldbook entry injection mode")
			}
			if entry.InjectionDepth < 0 || entry.InjectionDepth > 20 {
				return fmt.Errorf("worldbook entry injection depth must be between 0 and 20")
			}
			if entry.ID == "" || utf8.RuneCountInString(entry.ID) > 256 || utf8.RuneCountInString(entry.Name) > 256 || utf8.RuneCountInString(entry.Content) > 64000 {
				return fmt.Errorf("worldbook entry exceeds supported limits")
			}
			switch entry.SecondaryLogic {
			case "", "none", "any", "all", "not_any", "not_all":
			default:
				return fmt.Errorf("invalid worldbook secondary_logic")
			}
			for _, keys := range [][]string{entry.Keys, entry.SecondaryKeys} {
				if len(keys) > 64 {
					return fmt.Errorf("worldbook supports at most 64 keys per list")
				}
				for _, key := range keys {
					if utf8.RuneCountInString(key) > 256 {
						return fmt.Errorf("worldbook key exceeds 256 characters")
					}
					if len(entry.Issues) == 0 {
						if _, err := CompileWorldbookKey(key, entry.CaseSensitive); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	data, err := kitutil.Marshal(c)
	if err != nil {
		return err
	}
	if len(data) > MaxWorldbookBytes {
		return fmt.Errorf("worldbook exceeds 6 MiB")
	}
	return nil
}

// CompileWorldbookKey returns nil for plain keys. Slash expressions use RE2;
// global matching has no effect on a boolean trigger and is safely ignored.
func CompileWorldbookKey(key string, caseSensitive bool) (*regexp.Regexp, error) {
	if !strings.HasPrefix(key, "/") {
		return nil, nil
	}
	end := strings.LastIndex(key, "/")
	if end < 1 {
		return nil, fmt.Errorf("invalid worldbook regex")
	}
	flags := ""
	if !caseSensitive {
		flags = "i"
	}
	for _, flag := range key[end+1:] {
		switch flag {
		case 'i', 'm', 's':
			if !strings.ContainsRune(flags, flag) {
				flags += string(flag)
			}
		case 'g':
		default:
			return nil, fmt.Errorf("unsupported worldbook regex flag %q", flag)
		}
	}
	pattern := key[1:end]
	if flags != "" {
		pattern = "(?" + flags + ")" + pattern
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid worldbook regex: %w", err)
	}
	return expression, nil
}
