package settings

// WebhookEntry is an outgoing webhook in a configuration document: never its address or secret, which stay on the instance.
type WebhookEntry struct {
	Name    string   `yaml:"name"`
	Preset  string   `yaml:"preset"`
	Enabled bool     `yaml:"enabled"`
	Events  []string `yaml:"events,omitempty"`
	// Libraries are "media_type/name"; empty means every library.
	Libraries    []string `yaml:"libraries,omitempty"`
	IncludePaths bool     `yaml:"include_paths,omitempty"`
	IncludeAdult bool     `yaml:"include_adult,omitempty"`
}

// IncomingWebhookEntry is an incoming webhook in a document: never its token or the token's hash.
type IncomingWebhookEntry struct {
	Name    string   `yaml:"name"`
	Enabled bool     `yaml:"enabled"`
	Actions []string `yaml:"actions,omitempty"`
	// Libraries are "media_type/name"; MediaServer is a connection's name.
	Libraries   []string `yaml:"libraries,omitempty"`
	MediaServer string   `yaml:"media_server,omitempty"`
}

// ProviderDefinitionEntry is a person's own definition file (the `custom/` folder), carried as its text.
type ProviderDefinitionEntry struct {
	Kind string `yaml:"kind"`
	Key  string `yaml:"key"`
	File string `yaml:"file"`
}
