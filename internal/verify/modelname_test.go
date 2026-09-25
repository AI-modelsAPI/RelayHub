package verify

import "testing"

// Relays rename models freely; a probe must only condemn a channel when the
// response names another family, tier or version (AUDIT §5 B1).
func TestCompareModelNames(t *testing.T) {
	cases := []struct {
		requested, reported, want string
	}{
		// Honest spellings: snapshots, aliases, vendor prefixes, Bedrock ids.
		{"claude-sonnet-4-5", "claude-sonnet-4-5-20250929", ModelSame},
		{"claude-3-5-sonnet-latest", "claude-3-5-sonnet-20241022", ModelSame},
		{"claude-sonnet-4-0", "claude-sonnet-4-20250514", ModelSame},
		{"claude-4-sonnet", "claude-sonnet-4", ModelSame},
		{"anthropic/claude-3.5-sonnet", "claude-3-5-sonnet-20241022", ModelSame},
		{"claude-3-5-sonnet-20241022", "us.anthropic.claude-3-5-sonnet-20241022-v2:0", ModelSame},
		{"claude-sonnet-4-5", "claude-sonnet-4-5-thinking", ModelSame},
		{"GPT-4o", "gpt-4o-2024-08-06", ModelSame},
		{"gemini-2.5-pro", "gemini-2.5-pro-preview-05-06", ModelSame},
		{"kimi-k2", "kimi-k2-0905-preview", ModelSame},
		{"gpt-4", "gpt-4-0613", ModelSame},
		{"claude-opus-4-1", "", ModelSame},
		// Substitutions: another family, tier or version behind the name.
		{"claude-sonnet-4-5", "glm-4.6", ModelDifferent},
		{"claude-3-opus", "gpt-4o", ModelDifferent},
		{"gpt-4o", "gpt-4o-mini-2024-07-18", ModelDifferent},
		{"claude-opus-4-1", "claude-sonnet-4-5", ModelDifferent},
		{"claude-opus-4-1", "claude-opus-4-20250514", ModelDifferent},
		{"claude-sonnet-4-5", "claude-3-5-sonnet-20241022", ModelDifferent},
		{"gemini-2.5-pro", "gemini-2.5-flash", ModelDifferent},
		{"deepseek-reasoner", "deepseek-chat", ModelDifferent},
		{"o3", "o4-mini", ModelDifferent},
		{"cc-opus", "claude-sonnet-4-5", ModelDifferent},
		// A different spelling without a conflict is informational only.
		{"deepseek-chat", "deepseek-v3.1", ModelRenamed},
		{"my-sonnet", "claude-sonnet-4-5", ModelRenamed},
	}
	for _, c := range cases {
		if got := CompareModelNames(c.requested, c.reported); got != c.want {
			t.Errorf("CompareModelNames(%q, %q) = %q, want %q", c.requested, c.reported, got, c.want)
		}
	}
}
