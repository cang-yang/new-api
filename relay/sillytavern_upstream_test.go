package relay

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is an opt-in, strict live differential test. It measures every preset
// in the supplied archive against real SillyTavern captures and fails on any
// provider-bound mismatch. Preset scripts, persistent Tavern state and UI
// extensions may still be missing from the New API context supplied here.
func TestSillyTavernArchiveProviderDifferential(t *testing.T) {
	archivePath, captureURL := os.Getenv("ST_PRESET_ZIP"), os.Getenv("ST_ORACLE_CAPTURE_URL")
	if archivePath == "" || captureURL == "" {
		t.Skip("ST_PRESET_ZIP and ST_ORACLE_CAPTURE_URL are required")
	}
	response, err := http.Get(captureURL)
	require.NoError(t, err)
	defer response.Body.Close()
	var captures []struct {
		Tag  string          `json:"tag"`
		Body json.RawMessage `json:"body"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&captures))
	archive, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer archive.Close()
	fileByTag := map[string]string{
		"RONG":        "RONG × Rhea｜R寶 V10-2(831) (1).json",
		"Kemini":      "Kemini_Dramatron_v3.1.json",
		"Kedai":       "8.14【可待-从头越】 直出版.json",
		"V19.2-clean": "[主预设] V19.2 狐神抚 · 毓忻.json",
		"V19.3":       "[主预设] V19.3 狐神抚 · 毓忻.json",
		"Wanxiang":    "万象谱v1.0-1.json",
	}
	userMessage := "<reader_input>\n你好，这是一次预设差分测试。\n</reader_input>"
	randomID := regexp.MustCompile(`[0-9A-F]{64}`)
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	seen := make(map[string]bool, len(fileByTag))
	for _, captured := range captures {
		filename, known := fileByTag[captured.Tag]
		if !known {
			continue
		}
		seen[captured.Tag] = true
		t.Run(captured.Tag, func(t *testing.T) {
			var preset []byte
			for _, file := range archive.File {
				if file.Name == filename {
					reader, openErr := file.Open()
					require.NoError(t, openErr)
					preset, err = io.ReadAll(reader)
					require.NoError(t, reader.Close())
					require.NoError(t, err)
					break
				}
			}
			require.NotEmpty(t, preset)
			var oracle struct {
				Messages []dto.Message `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(captured.Body, &oracle))
			var oracleFields map[string]any
			require.NoError(t, json.Unmarshal(captured.Body, &oracleFields))
			delete(oracleFields, "messages")
			bestScore := -1
			bestName := ""
			var bestRequest *dto.GeneralOpenAIRequest
			for _, candidate := range []struct {
				name    string
				message string
			}{
				{"wrapped", userMessage},
				{"raw", "你好，这是一次预设差分测试。"},
			} {
				stream := true
				request := &dto.GeneralOpenAIRequest{Model: "oracle-test", Stream: &stream, Messages: []dto.Message{{Role: "user", Content: candidate.message}}}
				compiled, _, compileErr := service.CompileSillyTavernPreset(&dto.SillyTavernPresetConfig{
					Preset: preset, User: "阳", Char: "Assistant", PostProcessing: "strict", ReferenceSource: "custom",
				}, request, service.SillyTavernContext{LastUserMessage: "你好，这是一次预设差分测试。"})
				if compileErr != nil {
					t.Logf("candidate=%s compile_error=%v", candidate.name, compileErr)
					continue
				}
				score := 0
				for index := range min(len(oracle.Messages), len(compiled.Messages)) {
					if oracle.Messages[index].Role == compiled.Messages[index].Role {
						score++
					}
					left, leftOK := oracle.Messages[index].Content.(string)
					right, rightOK := compiled.Messages[index].Content.(string)
					if leftOK && rightOK && randomID.ReplaceAllString(left, "<RANDOM>") == randomID.ReplaceAllString(right, "<RANDOM>") {
						score += 4
					}
				}
				if len(oracle.Messages) == len(compiled.Messages) {
					score += 10
				}
				if score > bestScore {
					bestScore, bestName, bestRequest = score, candidate.name, compiled
				}
			}
			if bestRequest == nil {
				t.Log("no compiled request")
				return
			}
			upstreamBody := make(chan []byte, 1)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				if readErr == nil {
					upstreamBody <- body
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"capture only","type":"invalid_request_error"}}`)
			}))
			defer provider.Close()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeOpenAI)
			ctx.Set(string(constant.ContextKeyChannelBaseUrl), provider.URL)
			ctx.Set(string(constant.ContextKeyOriginalModel), "oracle-test")
			info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions,
				OriginModelName: "oracle-test", RequestURLPath: "/v1/chat/completions", Request: bestRequest}
			if relayErr := TextHelper(ctx, info); relayErr == nil {
				t.Log("mock unexpectedly accepted request")
			}
			var body []byte
			select {
			case body = <-upstreamBody:
			default:
				t.Log("provider did not receive request")
				return
			}
			var final struct {
				Messages []dto.Message `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(body, &final))
			var finalFields map[string]any
			require.NoError(t, json.Unmarshal(body, &finalFields))
			delete(finalFields, "messages")
			roleMatches, contentMatches := 0, 0
			var differentIndices []string
			for index := range min(len(oracle.Messages), len(final.Messages)) {
				if oracle.Messages[index].Role == final.Messages[index].Role {
					roleMatches++
				}
				left, leftOK := oracle.Messages[index].Content.(string)
				right, rightOK := final.Messages[index].Content.(string)
				if leftOK && rightOK && randomID.ReplaceAllString(left, "<RANDOM>") == randomID.ReplaceAllString(right, "<RANDOM>") {
					contentMatches++
				} else {
					differentIndices = append(differentIndices, fmt.Sprint(index))
					if leftOK && rightOK {
						left = randomID.ReplaceAllString(left, "<RANDOM>")
						right = randomID.ReplaceAllString(right, "<RANDOM>")
						position := firstTextDifference(left, right)
						t.Logf("message[%d] A_bytes=%d B_bytes=%d first_diff_byte=%d A_near=%q B_near=%q", index,
							len(left), len(right), position, textNear(left, position), textNear(right, position))
					}
				}
			}
			t.Logf("candidate=%s A_messages=%d B_messages=%d role_matches=%d content_matches=%d different_indices=%s fields_equal=%v A_roles=%s B_roles=%s", bestName,
				len(oracle.Messages), len(final.Messages), roleMatches, contentMatches, strings.Join(differentIndices, ","),
				reflect.DeepEqual(oracleFields, finalFields), joinedRoles(oracle.Messages), joinedRoles(final.Messages))
			if !reflect.DeepEqual(oracleFields, finalFields) {
				t.Logf("A_fields=%v B_fields=%v", oracleFields, finalFields)
			}
			if len(oracle.Messages) != len(final.Messages) || roleMatches != len(oracle.Messages) ||
				contentMatches != len(oracle.Messages) || !reflect.DeepEqual(oracleFields, finalFields) {
				t.Errorf("New API provider-bound request is not identical to the SillyTavern oracle for %s", captured.Tag)
			}
		})
	}
	for tag := range fileByTag {
		require.Truef(t, seen[tag], "missing real SillyTavern provider capture for %s", tag)
	}
}

func firstTextDifference(a, b string) int {
	for index := 0; index < min(len(a), len(b)); index++ {
		if a[index] != b[index] {
			return index
		}
	}
	return min(len(a), len(b))
}

func textNear(value string, position int) string {
	start := max(0, position-24)
	end := min(len(value), position+48)
	return value[start:end]
}

func joinedRoles(messages []dto.Message) string {
	roles := make([]string, len(messages))
	for index, message := range messages {
		roles[index] = message.Role
	}
	return strings.Join(roles, ",")
}

// Uses the same ARGO reference captured from real SillyTavern, but checks
// the final JSON observed by a provider after the New API relay path.
func TestSillyTavernARGOProviderGolden(t *testing.T) {
	archivePath := os.Getenv("ST_PRESET_ZIP")
	if archivePath == "" {
		t.Skip("ST_PRESET_ZIP is required")
	}
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	fixtureBytes, err := os.ReadFile("../service/testdata/sillytavern-golden/ARGO-1.6/reference.json")
	require.NoError(t, err)
	var fixture struct {
		Context struct {
			User               string `json:"user"`
			Char               string `json:"char"`
			CurrentUserMessage string `json:"current_user_message"`
		} `json:"context"`
		ProviderFields map[string]any        `json:"provider_fields"`
		Modes          map[string][][]string `json:"modes"`
	}
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	archive, err := zip.OpenReader(archivePath)
	require.NoError(t, err)
	defer archive.Close()
	var preset []byte
	for _, file := range archive.File {
		if file.Name == "ARGO-1.6.json" {
			reader, openErr := file.Open()
			require.NoError(t, openErr)
			preset, err = io.ReadAll(reader)
			require.NoError(t, reader.Close())
			require.NoError(t, err)
			break
		}
	}
	require.NotEmpty(t, preset)
	randomID := regexp.MustCompile(`[0-9A-F]{64}`)
	for _, mode := range []string{"none", "strict"} {
		t.Run(mode, func(t *testing.T) {
			upstreamBody := make(chan []byte, 1)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, readErr := io.ReadAll(r.Body)
				if readErr == nil {
					upstreamBody <- body
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"capture only","type":"invalid_request_error"}}`)
			}))
			defer provider.Close()

			stream := true
			request := &dto.GeneralOpenAIRequest{Model: "oracle-test", Stream: &stream, Messages: []dto.Message{{Role: "user", Content: fixture.Context.CurrentUserMessage}}}
			compiled, _, compileErr := service.CompileSillyTavernPreset(&dto.SillyTavernPresetConfig{
				Preset: preset, User: fixture.Context.User, Char: fixture.Context.Char,
				PostProcessing: mode, ReferenceSource: "custom",
			}, request, service.SillyTavernContext{})
			require.NoError(t, compileErr)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeOpenAI)
			ctx.Set(string(constant.ContextKeyChannelBaseUrl), provider.URL)
			ctx.Set(string(constant.ContextKeyOriginalModel), "oracle-test")
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeChatCompletions, OriginModelName: "oracle-test",
				RequestURLPath: "/v1/chat/completions", Request: compiled,
			}
			require.NotNil(t, TextHelper(ctx, info), "mock provider rejects after capturing")
			var body []byte
			select {
			case body = <-upstreamBody:
			default:
				t.Fatal("provider did not receive a request")
			}
			var final struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(body, &final))
			golden := fixture.Modes[mode]
			require.Len(t, final.Messages, len(golden), "provider-bound message boundaries/count")
			for index, message := range final.Messages {
				require.Len(t, golden[index], 2)
				assert.Equalf(t, golden[index][0], message.Role, "message[%d].role", index)
				hash := sha256.Sum256([]byte(randomID.ReplaceAllString(message.Content, "<RANDOM>")))
				assert.Equalf(t, golden[index][1], fmt.Sprintf("%x", hash), "message[%d].content SHA-256", index)
			}
			var fields map[string]any
			require.NoError(t, json.Unmarshal(body, &fields))
			delete(fields, "messages")
			assert.Equal(t, fixture.ProviderFields, fields, "final provider request fields")
		})
	}
}

// Captures the JSON actually handed to an OpenAI-compatible provider after
// preset compilation, model mapping, adaptor conversion, disabled-field
// removal, and parameter override. The provider deliberately rejects the
// request so this test never performs billing or consumes a real model.
func TestSillyTavernPostProcessingProviderRequest(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	preset := []byte(`{
		"top_k":40,"min_p":0.1,"repetition_penalty":1.1,
		"prompts":[
			{"identifier":"one","role":"system","content":"one"},
			{"identifier":"two","role":"system","content":"two"},
			{"identifier":"chatHistory","marker":true},
			{"identifier":"late","role":"system","content":"late"}
		],
		"prompt_order":[{"character_id":100001,"order":[
			{"identifier":"one","enabled":true},
			{"identifier":"two","enabled":true},
			{"identifier":"chatHistory","enabled":true},
			{"identifier":"late","enabled":true}
		]}]
	}`)
	for _, tc := range []struct {
		mode     string
		expected []dto.Message
	}{
		{"none", []dto.Message{{Role: "system", Content: "one"}, {Role: "system", Content: "two"}, {Role: "user", Content: "hello"}, {Role: "system", Content: "late"}}},
		{"strict", []dto.Message{{Role: "system", Content: "one\n\ntwo"}, {Role: "user", Content: "hello\n\nlate"}}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			upstreamBody := make(chan []byte, 1)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v1/chat/completions", r.URL.Path)
				body, readErr := io.ReadAll(r.Body)
				if readErr == nil {
					upstreamBody <- body
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"capture only","type":"invalid_request_error"}}`)
			}))
			defer provider.Close()

			request := &dto.GeneralOpenAIRequest{Model: "oracle-test", Messages: []dto.Message{{Role: "user", Content: "hello"}}}
			compiled, _, err := service.CompileSillyTavernPreset(&dto.SillyTavernPresetConfig{Preset: preset, PostProcessing: tc.mode, ReferenceSource: "custom"}, request, service.SillyTavernContext{})
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Set(string(constant.ContextKeyChannelType), constant.ChannelTypeOpenAI)
			ctx.Set(string(constant.ContextKeyChannelBaseUrl), provider.URL)
			ctx.Set(string(constant.ContextKeyOriginalModel), "oracle-test")
			info := &relaycommon.RelayInfo{
				RelayMode: relayconstant.RelayModeChatCompletions, OriginModelName: "oracle-test",
				RequestURLPath: "/v1/chat/completions", Request: compiled,
			}
			require.NotNil(t, TextHelper(ctx, info), "mock provider rejects after capturing")
			select {
			case body := <-upstreamBody:
				var upstream struct {
					Model    string        `json:"model"`
					Messages []dto.Message `json:"messages"`
					TopK     *int          `json:"top_k"`
				}
				require.NoError(t, json.Unmarshal(body, &upstream))
				assert.Equal(t, "oracle-test", upstream.Model)
				assert.Equal(t, tc.expected, upstream.Messages)
				assert.Nil(t, upstream.TopK, "Custom source must omit unsupported top_k")
			default:
				t.Fatal("provider did not receive a request")
			}
		})
	}
}
