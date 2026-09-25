package verify

// Model name verdicts.
const (
	ModelSame      = "same"
	ModelRenamed   = "renamed"
	ModelDifferent = "different"
)

// CompareModelNames compares a requested and a reported model name. Not
// implemented yet.
func CompareModelNames(requested, reported string) string { return ModelSame }
