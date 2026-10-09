package ai

import (
	"testing"
)

func env(m map[string]string) Env { return func(k string) string { return m[k] } }

func TestProvidersWithoutKeys(t *testing.T) {
	ps := Providers(env(nil))
	if len(ps) != 2 || ps[0].ID() != "claude" || ps[1].ID() != "openai" {
		t.Fatalf("providers = %v", ps)
	}
	for _, p := range ps {
		if p.Configured() {
			t.Errorf("%s: Configured() without a key", p.ID())
		}
		if p.(KeyHinter).MaskedKey() != "" {
			t.Errorf("%s: MaskedKey() without a key", p.ID())
		}
	}
	c, o := ps[0].(*Claude), ps[1].(*OpenAI)
	if c.Model() != DefaultClaudeModel || c.Effort() != DefaultClaudeEffort || c.DisplayName() != "Claude" {
		t.Errorf("claude = %s %s %s", c.DisplayName(), c.Model(), c.Effort())
	}
	if o.Model() != DefaultOpenAIModel || o.Effort() != DefaultOpenAIEffort || o.DisplayName() != "OpenAI" {
		t.Errorf("openai = %s %q %s", o.DisplayName(), o.Model(), o.Effort())
	}
}

func TestProvidersWithKeys(t *testing.T) {
	e := env(map[string]string{
		EnvAnthropicKey: "sk-ant-api03-abcdefghijklmnop-WXYZ",
		EnvOpenAIKey:    " short ",
		EnvClaudeModel:  "claude-sonnet-5-5",
		EnvClaudeEffort: "high",
		EnvOpenAIModel:  "some-model",
		EnvOpenAIEffort: "low",
		// Global names must be ignored.
		"ANTHROPIC_API_KEY": "global",
		"OPENAI_API_KEY":    "global",
	})
	c, o := NewClaude(e), NewOpenAI(e)
	if !c.Configured() || c.MaskedKey() != "••••WXYZ" || c.Model() != "claude-sonnet-5-5" || c.Effort() != "high" {
		t.Errorf("claude: %v %q %q %q", c.Configured(), c.MaskedKey(), c.Model(), c.Effort())
	}
	if !o.Configured() || o.MaskedKey() != "••••" || o.Model() != "some-model" || o.Effort() != "low" {
		t.Errorf("openai: %v %q %q %q", o.Configured(), o.MaskedKey(), o.Model(), o.Effort())
	}
	global := env(map[string]string{"ANTHROPIC_API_KEY": "global", "OPENAI_API_KEY": "global"})
	if NewClaude(global).Configured() || NewOpenAI(global).Configured() {
		t.Error("global API key variables must not be used")
	}
}

func TestFind(t *testing.T) {
	ps := Providers(env(nil))
	if Find(ps, "openai") != ps[1] || Find(ps, "nope") != nil {
		t.Error("Find")
	}
}

func TestSupportsDefaultFallback(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-opus-5-5": true, "claude-opus-5": true, "claude-fable-5-1": true, "claude-sonnet-5-5": true,
		"claude-haiku-5-5": false, "claude-sonnet-5": false, "claude-opus-4-8": false,
	} {
		if got := supportsDefaultFallback(model); got != want {
			t.Errorf("supportsDefaultFallback(%q) = %v", model, got)
		}
	}
}
