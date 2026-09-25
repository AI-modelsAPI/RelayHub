package verify

import (
	"regexp"
	"sort"
	"strings"
)

// Model-name comparison for authenticity probes (AUDIT §5 B1).
//
// Relays rename models freely: dated snapshots, "-latest" aliases, vendor
// prefixes ("anthropic/claude-3.5-sonnet"), Bedrock ids, reordered words
// ("claude-4-sonnet"). Exact string equality would condemn honest channels.
// What a probe has to catch is a different model behind the requested
// name: another vendor family (claude → glm), another tier (gpt-4o →
// gpt-4o-mini, opus → sonnet) or another version (claude-sonnet-4-5 →
// claude-3-5-sonnet).

// Model name verdicts returned by CompareModelNames.
const (
	// ModelSame: the names denote the same model.
	ModelSame = "same"
	// ModelRenamed: spelled differently, but nothing conflicts (e.g. a
	// relay alias without a version). Informational only.
	ModelRenamed = "renamed"
	// ModelDifferent: family, tier or version conflict.
	ModelDifferent = "different"
)

// modelFamilies maps name tokens to a vendor family. A token matches when
// it equals the key or continues it with a digit ("qwen3", "gpt4").
var modelFamilies = [][2]string{
	{"claude", "claude"},
	{"chatgpt", "gpt"}, {"gpt", "gpt"},
	{"o1", "o1"}, {"o3", "o3"}, {"o4", "o4"},
	{"gemini", "gemini"}, {"gemma", "gemma"},
	{"deepseek", "deepseek"},
	{"qwen", "qwen"}, {"qwq", "qwen"},
	{"chatglm", "glm"}, {"glm", "glm"},
	{"kimi", "kimi"}, {"moonshot", "kimi"},
	{"llama", "llama"},
	{"mistral", "mistral"}, {"mixtral", "mistral"}, {"codestral", "mistral"},
	{"grok", "grok"},
	{"doubao", "doubao"},
	{"ernie", "ernie"},
	{"minimax", "minimax"}, {"abab", "minimax"},
	{"command", "command"},
	{"hunyuan", "hunyuan"},
}

// modelTiers are size/quality tiers: serving one tier under another's name
// is exactly the substitution the probe exists to catch.
var modelTiers = map[string]bool{
	"opus": true, "sonnet": true, "haiku": true,
	"mini": true, "nano": true, "micro": true, "tiny": true, "small": true, "medium": true, "large": true,
	"flash": true, "lite": true, "pro": true, "turbo": true, "max": true, "plus": true, "ultra": true,
	"instant": true, "air": true, "fast": true,
	"coder": true, "codex": true, "reasoner": true, "vision": true,
}

// modelQualifiers carry no identity (release channel, mode, packaging).
var modelQualifiers = map[string]bool{
	"latest": true, "preview": true, "exp": true, "experimental": true, "beta": true, "stable": true,
	"thinking": true, "instruct": true, "chat": true, "it": true, "hf": true, "online": true, "default": true,
}

var (
	bedrockRegion  = regexp.MustCompile(`^(us|eu|apac|global|jp|au|ca)\.`)
	bedrockVendor  = regexp.MustCompile(`^(anthropic|meta|amazon|mistral|cohere|ai21|deepseek|qwen|openai|writer)\.`)
	bedrockVersion = regexp.MustCompile(`-v\d+:\d+$`)
	isoDate        = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	nameSeparators = strings.NewReplacer(".", "-", "_", "-", ":", "-", " ", "-")
)

type modelName struct {
	norm     string
	family   string
	tiers    []string
	versions []string
}

func parseModelName(raw string) modelName {
	s := strings.ToLower(strings.TrimSpace(raw))
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:] // "anthropic/claude-…", "models/gemini-…"
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i] // Vertex "claude-3-5-sonnet-v2@20241022"
	}
	s = bedrockRegion.ReplaceAllString(s, "")
	s = bedrockVendor.ReplaceAllString(s, "")
	s = bedrockVersion.ReplaceAllString(s, "")
	s = isoDate.ReplaceAllString(s, "")
	s = nameSeparators.Replace(s)

	var p modelName
	var kept []string
	for _, tok := range strings.Split(s, "-") {
		if tok == "" || modelQualifiers[tok] || isSnapshotToken(tok) {
			continue
		}
		kept = append(kept, tok)
		if fam, rest, ok := familyOf(tok); ok {
			if p.family == "" {
				p.family = fam
			}
			if rest != "" {
				p.versions = append(p.versions, rest)
			}
			continue
		}
		if modelTiers[tok] {
			p.tiers = append(p.tiers, tok)
			continue
		}
		if strings.ContainsAny(tok, "0123456789") {
			p.versions = append(p.versions, tok)
		}
	}
	// "claude-sonnet-4-0" is Anthropic's alias spelling of "claude-sonnet-4".
	if n := len(p.versions); n > 1 && p.versions[n-1] == "0" {
		p.versions = p.versions[:n-1]
	}
	sort.Strings(p.tiers)
	p.norm = strings.Join(kept, "-")
	return p
}

// isSnapshotToken reports date/snapshot suffixes: 20250929, 0613, 002, and
// zero-padded month/day pairs such as the "05-06" of a preview name.
func isSnapshotToken(tok string) bool {
	for _, r := range tok {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(tok) >= 3 || (len(tok) == 2 && tok[0] == '0')
}

func familyOf(tok string) (family, rest string, ok bool) {
	for _, f := range modelFamilies {
		key := f[0]
		if tok == key {
			return f[1], "", true
		}
		if len(tok) > len(key) && strings.HasPrefix(tok, key) && tok[len(key)] >= '0' && tok[len(key)] <= '9' {
			return f[1], tok[len(key):], true
		}
	}
	return "", "", false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CompareModelNames compares the model a probe asked for with the model the
// response claims to be. An empty name on either side compares as the same:
// there is nothing to judge.
func CompareModelNames(requested, reported string) string {
	a, b := parseModelName(requested), parseModelName(reported)
	if a.norm == "" || b.norm == "" || a.norm == b.norm {
		return ModelSame
	}
	if a.family != "" && b.family != "" {
		if a.family != b.family || !equalStrings(a.tiers, b.tiers) {
			return ModelDifferent
		}
		if len(a.versions) > 0 && len(b.versions) > 0 {
			if !equalStrings(a.versions, b.versions) {
				return ModelDifferent
			}
			return ModelSame
		}
		return ModelRenamed
	}
	// A custom alias on one side: only conflicting tiers are conclusive
	// ("cc-opus" answered by a sonnet).
	if len(a.tiers) > 0 && len(b.tiers) > 0 && !equalStrings(a.tiers, b.tiers) {
		return ModelDifferent
	}
	return ModelRenamed
}
