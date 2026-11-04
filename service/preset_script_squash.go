package service

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

type presetChatSquash struct {
	Enabled           bool   `json:"enabled"`
	Conditional       bool   `json:"conditional_enabled"`
	ConditionalTag    string `json:"conditional_tag"`
	HistoryOnly       bool   `json:"separate_chat_history"`
	ParseClewd        bool   `json:"parse_clewd"`
	UserRoleSystem    bool   `json:"user_role_system"`
	Role              string `json:"role"`
	UserPrefix        string `json:"user_prefix"`
	UserSuffix        string `json:"user_suffix"`
	CharPrefix        string `json:"char_prefix"`
	CharSuffix        string `json:"char_suffix"`
	SystemPrefix      string `json:"prefix_system"`
	SystemSuffix      string `json:"suffix_system"`
	SeparatorEnabled  bool   `json:"enable_squashed_separator"`
	SeparatorRegex    bool   `json:"squashed_separator_regex"`
	Separator         string `json:"squashed_separator_string"`
	PostScriptEnabled bool   `json:"squashed_post_script_enable"`
	PostScript        string `json:"squashed_post_script"`
	Resplit           bool   `json:"re_split"`
	StopEnabled       bool   `json:"enable_stop_string"`
}

func applyPresetChatSquash(settings presetChatSquash, request *dto.GeneralOpenAIRequest, macro *presetMacroContext, deadline time.Time) error {
	if !settings.Enabled {
		return nil
	}
	if settings.HistoryOnly || settings.SeparatorRegex || settings.Resplit || settings.StopEnabled {
		return fmt.Errorf("SPreset ChatSquash history-only, regex separators, re_split and stop-string modes are not supported by spreset-v1")
	}
	if !slices.Contains([]string{"system", "user", "assistant", "follow"}, settings.Role) {
		return fmt.Errorf("SPreset ChatSquash requires system, user, assistant or follow role")
	}
	if settings.Conditional {
		found := false
		for index := range request.Messages {
			text, ok := request.Messages[index].Content.(string)
			if ok && settings.ConditionalTag != "" && strings.Contains(text, settings.ConditionalTag) {
				request.Messages[index].Content = strings.ReplaceAll(text, settings.ConditionalTag, "")
				found = true
			}
		}
		if !found {
			return nil
		}
	}
	prefixes := map[string]string{"user": macro.expand(settings.UserPrefix, 0), "assistant": macro.expand(settings.CharPrefix, 0), "system": macro.expand(settings.SystemPrefix, 0)}
	suffixes := map[string]string{"user": macro.expand(settings.UserSuffix, 0), "assistant": macro.expand(settings.CharSuffix, 0), "system": macro.expand(settings.SystemSuffix, 0)}
	role := settings.Role
	if role == "follow" {
		role = request.Messages[0].Role
	}
	var output []dto.Message
	var text strings.Builder
	lastRole := ""
	flush := func() error {
		if lastRole == "" {
			return nil
		}
		text.WriteString(suffixes[lastRole])
		value := text.String()
		if len(value) > maxFilteredResponseBytes {
			return fmt.Errorf("SPreset ChatSquash text exceeds 8 MiB")
		}
		if settings.PostScriptEnabled {
			if err := runPresetScript(settings.PostScript, value, &value, deadline); err != nil {
				return fmt.Errorf("ChatSquash post-script: %w", err)
			}
		}
		output = append(output, dto.Message{Role: role, Content: value})
		text.Reset()
		lastRole = ""
		return nil
	}
	for _, message := range request.Messages {
		content, plain := message.Content.(string)
		boundary := !plain || message.Name != nil || message.Prefix != nil || message.Reasoning != nil ||
			message.ReasoningContent != nil || len(message.ToolCalls) > 0 || message.ToolCallId != "" ||
			len(message.Tools) > 0 || len(message.Annotations) > 0 || len(message.ThoughtSignature) > 0 ||
			!slices.Contains([]string{"system", "user", "assistant"}, message.Role)
		if plain {
			for _, marker := range []struct {
				enabled bool
				value   string
			}{{settings.SeparatorEnabled, settings.Separator}, {settings.ParseClewd, "<|no-trans|>"}} {
				if marker.enabled && marker.value != "" && strings.Contains(content, marker.value) {
					content = strings.Replace(content, marker.value, "", 1)
					boundary = true
					break
				}
			}
			message.Content = content
		}
		if boundary {
			if err := flush(); err != nil {
				return err
			}
			output = append(output, message)
			if settings.Role == "follow" && slices.Contains([]string{"system", "user", "assistant"}, message.Role) {
				role = message.Role
			}
			continue
		}
		if content == "" {
			continue
		}
		messageRole := message.Role
		if settings.UserRoleSystem && messageRole == "system" {
			messageRole = "user"
		}
		if lastRole == messageRole {
			text.WriteByte('\n')
		} else {
			text.WriteString(suffixes[lastRole])
			text.WriteString(prefixes[messageRole])
		}
		text.WriteString(content)
		lastRole = messageRole
	}
	if err := flush(); err != nil {
		return err
	}
	if len(output) == 0 {
		return fmt.Errorf("SPreset ChatSquash produced no messages")
	}
	request.Messages = output
	return nil
}
