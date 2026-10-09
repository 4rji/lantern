package ai

import (
	"context"
	"errors"
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
	if ps[0].Model() != DefaultClaudeModel || ps[0].DisplayName() != "Claude" {
		t.Errorf("claude = %s %s", ps[0].DisplayName(), ps[0].Model())
	}
	if ps[1].Model() != "" || ps[1].DisplayName() != "OpenAI" {
		t.Errorf("openai = %s %q", ps[1].DisplayName(), ps[1].Model())
	}
}

func TestProvidersWithKeys(t *testing.T) {
	e := env(map[string]string{
		EnvAnthropicKey: "sk-ant-api03-abcdefghijklmnop-WXYZ",
		EnvOpenAIKey:    " short ",
		EnvClaudeModel:  "claude-sonnet-5-5",
		EnvOpenAIModel:  "some-model",
		// Global names must be ignored.
		"ANTHROPIC_API_KEY": "global",
	})
	c, o := NewClaude(e), NewOpenAI(e)
	if !c.Configured() || c.MaskedKey() != "••••WXYZ" || c.Model() != "claude-sonnet-5-5" {
		t.Errorf("claude: %v %q %q", c.Configured(), c.MaskedKey(), c.Model())
	}
	if !o.Configured() || o.MaskedKey() != "••••" || o.Model() != "some-model" {
		t.Errorf("openai: %v %q %q", o.Configured(), o.MaskedKey(), o.Model())
	}
	if NewClaude(env(map[string]string{"ANTHROPIC_API_KEY": "global"})).Configured() {
		t.Error("ANTHROPIC_API_KEY must not be used")
	}
}

func TestAskNotImplemented(t *testing.T) {
	for _, p := range Providers(env(map[string]string{EnvAnthropicKey: "k", EnvOpenAIKey: "k"})) {
		if _, err := p.Ask(context.Background(), AskRequest{Question: "q"}); !errors.Is(err, ErrNotImplemented) {
			t.Errorf("%s: err = %v", p.ID(), err)
		}
	}
}

func TestFind(t *testing.T) {
	ps := Providers(env(nil))
	if Find(ps, "openai") != ps[1] || Find(ps, "nope") != nil {
		t.Error("Find")
	}
}
