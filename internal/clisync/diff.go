package clisync

type Target struct {
	CLI        string `json:"cli"`
	ConfigPath string `json:"config_path"`
	Exists     bool   `json:"exists"`
}

type DesiredState struct {
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
	Provider string `json:"provider"`
}

type Diff struct {
	Target      Target   `json:"target"`
	APIKey      string   `json:"api_key,omitempty"`
	OldContent  string   `json:"old_content"`
	NewContent  string   `json:"new_content"`
	ChangedKeys []string `json:"changed_keys"`
	HasChanges  bool     `json:"has_changes"`
}
