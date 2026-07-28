package config

type Config struct {
	AppId          uint32
	DepotID        uint32
	Branch         string
	TempDir        string
	OutputDir      string
	ManifestIDPath string
	TargetFiles    []string
}

func Default() Config {
	return Config{
		AppId:          730,
		DepotID:        2347770,
		Branch:         "public",
		TempDir:        "temp",
		OutputDir:      "static",
		ManifestIDPath: "",
		TargetFiles: []string{
			"resource/csgo_english.txt",
			"resource/csgo_spanish.txt",
			"resource/csgo_portuguese.txt",
			"scripts/items/items_game.txt",
		},
	}
}
