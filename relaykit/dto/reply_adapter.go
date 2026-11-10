package dto

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// ValidateJSONPointer deliberately supports only bounded, literal field paths.
func ValidateJSONPointer(pointer string) error {
	if len(pointer) > 2048 || pointer != "" && !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("reply field must be a JSON Pointer")
	}
	if pointer == "" {
		return nil
	}
	parts := strings.Split(pointer[1:], "/")
	if len(parts) > 32 {
		return fmt.Errorf("reply field exceeds 32 levels")
	}
	for _, part := range parts {
		for index := 0; index < len(part); index++ {
			if part[index] == '~' {
				if index+1 == len(part) || part[index+1] != '0' && part[index+1] != '1' {
					return fmt.Errorf("reply field has an invalid escape")
				}
				index++
			}
		}
		decoded := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if decoded == "__proto__" || decoded == "prototype" || decoded == "constructor" || decoded == "*" || decoded == "-" {
			return fmt.Errorf("reply field uses an unsupported segment")
		}
	}
	return nil
}

func (a *ReplyAdapter) Validate() error {
	if a == nil {
		return nil
	}
	if a.Version != 2 || len(a.Containers) > 32 {
		return fmt.Errorf("unsupported reply adapter version or size")
	}
	if a.StructuredOutput && len(a.Containers) == 0 {
		return fmt.Errorf("structured output conversion requires a declared reply source")
	}
	seen := map[string]bool{}
	for _, c := range a.Containers {
		if (c.SourcePromptID == "") != (c.SourceFingerprint == "") || len(c.SourcePromptID) > 256 || c.SourceFingerprint != "" && len(c.SourceFingerprint) != 64 {
			return fmt.Errorf("detected reply sources require a prompt identity and SHA-256 fingerprint")
		}
		if c.SourceFingerprint != "" {
			if _, err := hex.DecodeString(c.SourceFingerprint); err != nil {
				return fmt.Errorf("detected reply sources require a valid SHA-256 fingerprint")
			}
		}
		if !validSillyTavernToolName(c.Name) || seen[c.Name] || c.OutputKind != "text" && c.OutputKind != "json_object" {
			return fmt.Errorf("reply transports require unique names and an output kind")
		}
		seen[c.Name] = true
		if (c.ArgumentPointer != nil) == c.Formatter {
			return fmt.Errorf("choose either a reply field or the preset formatter")
		}
		if c.ArgumentPointer != nil {
			if err := ValidateJSONPointer(*c.ArgumentPointer); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateJSONTextPointers(paths []string) error {
	if len(paths) > 32 {
		return fmt.Errorf("at most 32 JSON text fields are supported")
	}
	for index, path := range paths {
		if path == "" {
			return fmt.Errorf("JSON root cannot be processed as a text field")
		}
		if err := ValidateJSONPointer(path); err != nil {
			return err
		}
		for _, other := range paths[:index] {
			if path == other || path == "" || other == "" || strings.HasPrefix(path, other+"/") || strings.HasPrefix(other, path+"/") {
				return fmt.Errorf("JSON text fields must not overlap")
			}
		}
	}
	return nil
}
