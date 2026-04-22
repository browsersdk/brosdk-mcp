package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type pageAgentAIConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

type pageAgentAIClient struct {
	mu     sync.Mutex
	config pageAgentAIConfig
}

func newPageAgentAIClient() *pageAgentAIClient {
	return &pageAgentAIClient{}
}

func (c *pageAgentAIClient) SetConfig(apiKey string, baseURL string, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config = pageAgentAIConfig{
		APIKey:  strings.TrimSpace(apiKey),
		BaseURL: strings.TrimSpace(baseURL),
		Model:   strings.TrimSpace(model),
	}
}

func (c *pageAgentAIClient) GetConfig() pageAgentAIConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config
}

func (c *pageAgentAIClient) ClearConfig() {
	c.SetConfig("", "", "")
}

func (c *pageAgentAIClient) HasConfig() bool {
	cfg := c.GetConfig()
	return strings.TrimSpace(cfg.APIKey) != "" && strings.TrimSpace(cfg.Model) != ""
}

func (e *Executor) SetPageAgentAIConfig(apiKey string, baseURL string, model string) {
	if e.pageAgentAI == nil {
		e.pageAgentAI = newPageAgentAIClient()
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if strings.TrimSpace(model) == "" {
		model = "gpt-5"
	}
	e.pageAgentAI.SetConfig(apiKey, baseURL, model)
}

func (e *Executor) ClearPageAgentAIConfig() {
	if e.pageAgentAI == nil {
		e.pageAgentAI = newPageAgentAIClient()
	}
	e.pageAgentAI.ClearConfig()
}

func (e *Executor) PageAgentAIConfigInfo() map[string]any {
	if e.pageAgentAI == nil {
		return map[string]any{
			"configured": false,
			"baseUrl":    "https://api.openai.com/v1",
			"model":      "gpt-5",
		}
	}
	cfg := e.pageAgentAI.GetConfig()
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := cfg.Model
	if model == "" {
		model = "gpt-5"
	}
	return map[string]any{
		"configured": strings.TrimSpace(cfg.APIKey) != "",
		"baseUrl":    baseURL,
		"model":      model,
	}
}

type responsesAPIRequest struct {
	Model string                  `json:"model"`
	Input []responsesAPIInputItem `json:"input"`
}

type responsesAPIInputItem struct {
	Role    string                    `json:"role"`
	Content []responsesAPIContentItem `json:"content"`
}

type responsesAPIContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesAPIResponse struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

var allowedAIProposalTools = map[string]struct{}{
	"browser_get_text":      {},
	"browser_aria_snapshot": {},
	"browser_click_by_ref":  {},
	"browser_type_by_ref":   {},
	"browser_screenshot":    {},
	"browser_wait_for_load": {},
}

func trimForAI(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "\n...[truncated]"
}

func extractJSONBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end >= start {
		return text[start : end+1]
	}
	return text
}

func validateAIProposal(proposal map[string]any) error {
	toolName, args, err := proposalToolAndArgs(proposal)
	if err != nil {
		return err
	}
	proposalType, _ := proposal["type"].(string)
	if strings.TrimSpace(proposalType) == "" {
		return fmt.Errorf("proposal missing type")
	}
	if _, ok := allowedAIProposalTools[toolName]; !ok {
		return fmt.Errorf("unsupported proposal tool %q", toolName)
	}
	intent, _ := proposal["intent"].(string)
	if strings.TrimSpace(intent) == "" {
		return fmt.Errorf("proposal missing intent")
	}
	reason, _ := proposal["reason"].(string)
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("proposal missing reason")
	}
	expectedOutcome, _ := proposal["expectedOutcome"].(string)
	if strings.TrimSpace(expectedOutcome) == "" {
		return fmt.Errorf("proposal missing expectedOutcome")
	}
	hints, exists := proposal["verificationHints"]
	if !exists {
		return fmt.Errorf("proposal missing verificationHints")
	}
	if hints != nil {
		if _, ok := hints.(map[string]any); !ok {
			return fmt.Errorf("proposal verificationHints must be an object")
		}
	}
	switch toolName {
	case "browser_click_by_ref":
		ref, _ := args["ref"].(string)
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("browser_click_by_ref proposal missing ref")
		}
	case "browser_type_by_ref":
		ref, _ := args["ref"].(string)
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("browser_type_by_ref proposal missing ref")
		}
	}
	return nil
}

func collectResponseText(resp responsesAPIResponse) string {
	var b strings.Builder
	for _, output := range resp.Output {
		for _, content := range output.Content {
			if strings.TrimSpace(content.Text) == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(content.Text)
		}
	}
	return b.String()
}

func (e *Executor) generateAIProposal(ctx context.Context, goal string, text string, snapshot string, lastTool string) (map[string]any, error) {
	if e.pageAgentAI == nil || !e.pageAgentAI.HasConfig() {
		return nil, fmt.Errorf("page agent ai config is not set")
	}
	cfg := e.pageAgentAI.GetConfig()

	systemPrompt := `You are a browser page agent planner.
Return exactly one JSON object and no markdown.
Choose the best next browser MCP tool from this allowlist only:
- browser_get_text
- browser_aria_snapshot
- browser_click_by_ref
- browser_type_by_ref
- browser_screenshot
- browser_wait_for_load

Rules:
- Prefer concrete ref-based actions when the snapshot contains a suitable ref.
- Only use browser_click_by_ref when you have a ref.
- Only use browser_type_by_ref when you have a ref, and include "text" when the goal clearly specifies what to enter.
- If the page needs more context, prefer browser_aria_snapshot or browser_get_text.
- If the page seems not ready, prefer browser_wait_for_load.
- Return JSON with keys: type, intent, tool, arguments, reason, confidence, expectedOutcome, needsVerification, verificationHints.
- Optional target object may include ref, role, name.
- verificationHints should be an object. Use lightweight hints such as valueVisible, targetName, targetGone, and successSignals when helpful.
`

	userPrompt := fmt.Sprintf("Goal:\n%s\n\nLast tool:\n%s\n\nVisible text:\n%s\n\nARIA snapshot:\n%s\n", trimForAI(goal, 1200), trimForAI(lastTool, 120), trimForAI(text, 5000), trimForAI(snapshot, 7000))

	reqBody := responsesAPIRequest{
		Model: cfg.Model,
		Input: []responsesAPIInputItem{
			{
				Role: "system",
				Content: []responsesAPIContentItem{
					{Type: "input_text", Text: systemPrompt},
				},
			},
			{
				Role: "user",
				Content: []responsesAPIContentItem{
					{Type: "input_text", Text: userPrompt},
				},
			},
		},
	}

	rawReq, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal ai request: %w", err)
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(rawReq))
	if err != nil {
		return nil, fmt.Errorf("build ai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: 45 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request ai proposal: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read ai response: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, fmt.Errorf("ai response status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var resp responsesAPIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("decode ai response: %w", err)
	}

	textOut := extractJSONBlock(collectResponseText(resp))
	if strings.TrimSpace(textOut) == "" {
		return nil, fmt.Errorf("ai response did not contain proposal text")
	}

	var proposal map[string]any
	if err := json.Unmarshal([]byte(textOut), &proposal); err != nil {
		return nil, fmt.Errorf("decode ai proposal json: %w", err)
	}
	if err := validateAIProposal(proposal); err != nil {
		return nil, err
	}
	return proposal, nil
}
