package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// EnvOpenAIEffort sets the reasoning effort (none, minimal, low, medium, high, xhigh, max).
const EnvOpenAIEffort = "LANTERN_OPENAI_EFFORT"

// DefaultOpenAIModel is OpenAI's most capable model per its docs (gpt-6-astra,
// released September 2026). The Responses API is OpenAI's recommended API for
// reasoning models and tool calling.
const DefaultOpenAIModel = "gpt-6-astra"

// DefaultOpenAIEffort matches Claude's default effort.
const DefaultOpenAIEffort = "medium"

// OpenAI answers with OpenAI's Responses API.
type OpenAI struct {
	envProvider
	effort  string
	baseURL string // tests only
}

// NewOpenAI reads LANTERN_OPENAI_API_KEY, LANTERN_OPENAI_MODEL and
// LANTERN_OPENAI_EFFORT. The global OPENAI_API_KEY is never used.
func NewOpenAI(env Env) *OpenAI {
	effort := strings.TrimSpace(env(EnvOpenAIEffort))
	if effort == "" {
		effort = DefaultOpenAIEffort
	}
	return &OpenAI{
		envProvider: newEnvProvider(env, EnvOpenAIKey, EnvOpenAIModel, DefaultOpenAIModel),
		effort:      effort,
	}
}

func (*OpenAI) ID() string          { return "openai" }
func (*OpenAI) DisplayName() string { return "OpenAI" }

// Effort is the reasoning effort sent with each request.
func (o *OpenAI) Effort() string { return o.effort }

// Ask runs the function-calling loop: each round sends the previous response ID
// plus the tool outputs, until the model answers without calling tools.
func (o *OpenAI) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	if !o.Configured() {
		return AskResponse{}, ErrNoKey
	}
	opts := []option.RequestOption{option.WithAPIKey(o.key)} // explicit key; never read from OPENAI_API_KEY
	if o.baseURL != "" {
		opts = append(opts, option.WithBaseURL(o.baseURL))
	}
	client := openai.NewClient(opts...)

	tools := make([]responses.ToolUnionParam, 0, len(Tools))
	for _, def := range Tools {
		tools = append(tools, responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
			Name:        def.Name,
			Description: openai.String(def.Description),
			Parameters:  def.Schema,
			Strict:      openai.Bool(false), // optional arguments aren't allowed in strict mode
		}})
	}
	params := responses.ResponseNewParams{
		Model:           o.model,
		Instructions:    openai.String(systemPrompt), // not inherited from previous responses, so sent every round
		Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String(userPrompt(req))},
		Tools:           tools,
		Reasoning:       shared.ReasoningParam{Effort: shared.ReasoningEffort(o.effort)},
		MaxOutputTokens: openai.Int(maxOutputTokens),
	}

	req.Tools.Progress("Asking OpenAI…")
	for round := 0; ; round++ {
		resp, err := client.Responses.New(ctx, params)
		if err != nil {
			return AskResponse{}, o.apiError(ctx, err)
		}
		if resp.Status == responses.ResponseStatusFailed {
			return AskResponse{}, &ProviderError{Provider: "OpenAI", Message: resp.Error.Message}
		}

		var calls []responses.ResponseFunctionToolCall
		for _, item := range resp.Output {
			switch item.Type {
			case "function_call":
				calls = append(calls, item.AsFunctionCall())
			case "message":
				for _, part := range item.AsMessage().Content {
					if part.Type == "refusal" {
						return AskResponse{}, &RefusalError{Provider: "OpenAI", Category: part.Refusal}
					}
				}
			}
		}

		if len(calls) == 0 {
			text := strings.TrimSpace(resp.OutputText())
			if resp.Status == responses.ResponseStatusIncomplete {
				text += "\n\n*(The answer was cut off: " + string(resp.IncompleteDetails.Reason) + ".)*"
			}
			return AskResponse{AnswerMarkdown: text, Sources: req.Tools.Sources(text)}, nil
		}
		if round >= MaxToolRounds {
			return AskResponse{}, ErrTooManySteps
		}

		outputs := make([]responses.ResponseInputItemUnionParam, 0, len(calls))
		for _, call := range calls {
			result, err := req.Tools.Call(ctx, call.Name, json.RawMessage(call.Arguments))
			if err != nil {
				result = "Error: " + err.Error()
			}
			outputs = append(outputs, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: openai.String(call.CallID),
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{OfString: openai.String(result)},
				},
			})
		}
		params.PreviousResponseID = openai.String(resp.ID)
		params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: outputs}
	}
}

func (o *OpenAI) apiError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return &ProviderError{
			Provider: "OpenAI",
			Status:   apiErr.StatusCode,
			Message:  friendlyStatus("OpenAI", EnvOpenAIKey, apiErr.StatusCode, apiErr.Message),
		}
	}
	return &ProviderError{Provider: "OpenAI", Message: err.Error()}
}
