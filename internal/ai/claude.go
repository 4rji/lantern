package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/toolrunner"
)

// EnvClaudeEffort sets Claude's effort level (low, medium, high, xhigh, max).
const EnvClaudeEffort = "LANTERN_CLAUDE_EFFORT"

// DefaultClaudeEffort is sent explicitly: the default model's own default is
// medium, and sending it makes the choice visible and configurable.
const DefaultClaudeEffort = "medium"

// Claude answers with Anthropic's API.
type Claude struct {
	envProvider
	effort  string
	baseURL string // tests only
}

// NewClaude reads LANTERN_ANTHROPIC_API_KEY, LANTERN_CLAUDE_MODEL and
// LANTERN_CLAUDE_EFFORT. The global ANTHROPIC_API_KEY is never used.
func NewClaude(env Env) *Claude {
	effort := strings.TrimSpace(env(EnvClaudeEffort))
	if effort == "" {
		effort = DefaultClaudeEffort
	}
	return &Claude{
		envProvider: newEnvProvider(env, EnvAnthropicKey, EnvClaudeModel, DefaultClaudeModel),
		effort:      effort,
	}
}

func (*Claude) ID() string          { return "claude" }
func (*Claude) DisplayName() string { return "Claude" }

// Effort is the effort level sent with each request.
func (c *Claude) Effort() string { return c.effort }

// Ask runs the tool loop with the SDK's beta tool runner and returns the final answer.
func (c *Claude) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	if !c.Configured() {
		return AskResponse{}, ErrNoKey
	}
	opts := []option.RequestOption{option.WithAPIKey(c.key)} // explicit key; never read from ANTHROPIC_API_KEY
	if c.baseURL != "" {
		opts = append(opts, option.WithBaseURL(c.baseURL))
	}
	client := anthropic.NewClient(opts...)

	tools, err := claudeTools(req.Tools)
	if err != nil {
		return AskResponse{}, err
	}
	params := anthropic.BetaMessageNewParams{
		Model:     c.model,
		MaxTokens: maxOutputTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(userPrompt(req))),
		},
		// Thinking is left unset: on the default model it is always on (adaptive).
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(c.effort)},
		// Cache the growing conversation, so each tool round re-reads earlier
		// documents from cache instead of paying for them again.
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
	}
	if supportsDefaultFallback(c.model) {
		// If a safety classifier declines, the API retries on the fallback model
		// Anthropic recommends for that refusal category, in the same call.
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	runner := client.Beta.Messages.NewToolRunner(tools, anthropic.BetaToolRunnerParams{
		BetaMessageNewParams: params,
		MaxIterations:        MaxToolRounds + 1, // tool rounds plus the final answer
	})
	req.Tools.Progress("Asking Claude…")
	var last *anthropic.BetaMessage
	for {
		msg, err := runner.NextMessage(ctx)
		if err != nil {
			return AskResponse{}, c.apiError(ctx, err)
		}
		if msg == nil {
			break
		}
		last = msg
		// Check for a refusal before reading content.
		if msg.StopReason == anthropic.BetaStopReasonRefusal {
			return AskResponse{}, &RefusalError{Provider: "Claude", Category: string(msg.StopDetails.Category)}
		}
	}
	if last == nil {
		return AskResponse{}, &ProviderError{Provider: "Claude", Message: "no response"}
	}
	if last.StopReason == anthropic.BetaStopReasonToolUse {
		return AskResponse{}, ErrTooManySteps
	}

	var answer strings.Builder
	for _, block := range last.Content {
		if text, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			answer.WriteString(text.Text)
		}
	}
	text := strings.TrimSpace(answer.String())
	if last.StopReason == anthropic.BetaStopReasonMaxTokens {
		text += "\n\n*(The answer was cut off because it reached the length limit.)*"
	}
	return AskResponse{AnswerMarkdown: text, Sources: req.Tools.Sources(text)}, nil
}

// claudeTools wraps the shared tool definitions for the tool runner. Tool
// errors become error results the model can see; they don't stop the loop.
func claudeTools(tb *Toolbox) ([]anthropic.BetaTool, error) {
	out := make([]anthropic.BetaTool, 0, len(Tools))
	for _, def := range Tools {
		name := def.Name
		tool, err := toolrunner.NewBetaToolFromBytes(name, def.Description, def.SchemaJSON(),
			func(ctx context.Context, input json.RawMessage) (anthropic.BetaToolResultBlockParamContentUnion, error) {
				result, err := tb.Call(ctx, name, input)
				if err != nil {
					return anthropic.BetaToolResultBlockParamContentUnion{}, err
				}
				return anthropic.BetaToolResultBlockParamContentUnion{
					OfText: &anthropic.BetaTextBlockParam{Text: result},
				}, nil
			})
		if err != nil {
			return nil, err
		}
		out = append(out, tool)
	}
	return out, nil
}

// supportsDefaultFallback reports whether the model accepts fallbacks: "default".
// Haiku has no server-side fallback, and older models don't know the parameter.
func supportsDefaultFallback(model string) bool {
	for _, prefix := range []string{"claude-opus-5", "claude-fable-5", "claude-mythos-5", "claude-sonnet-5-5"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}

func (c *Claude) apiError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return &ProviderError{
			Provider: "Claude",
			Status:   apiErr.StatusCode,
			Message:  friendlyStatus("Claude", EnvAnthropicKey, apiErr.StatusCode, apiErr.Error()),
		}
	}
	return &ProviderError{Provider: "Claude", Message: err.Error()}
}
