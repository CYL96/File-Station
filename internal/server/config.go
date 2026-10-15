package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

// LoadConfig applies defaults and either a strict JSON file or PASSWORD and UMASK.
// A nonempty path selects file-only configuration and ignores the environment.
// With an empty path, only the environment is applied; path selection belongs to the caller.
// New validates the resulting configuration and opens its resources.
func LoadConfig(path string) (Config, error) {
	config := &Config{IP: "127.0.0.1", Port: 8080, StateDir: "/var/lib/file-station", Umask: "0022"}
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, fmt.Errorf("open configuration %q: %w", path, err)
		}
		err = decodeConfigJSON(file, &config)
		file.Close()
		if err != nil {
			return Config{}, fmt.Errorf("decode configuration %q: %w", path, err)
		}
		if config == nil {
			return Config{}, fmt.Errorf("configuration %q must be a JSON object", path)
		}
		if config.Umask == "" {
			return Config{}, fmt.Errorf("configuration %q umask must not be empty", path)
		}
		return *config, nil
	}

	config.IP = "0.0.0.0"
	config.Password = os.Getenv("PASSWORD")
	if value, ok := os.LookupEnv("UMASK"); ok {
		_, err := strconv.ParseUint(value, 8, 9)
		if (len(value) != 3 && len(value) != 4) || err != nil {
			return Config{}, fmt.Errorf("UMASK must contain 3 or 4 octal digits with a value between 0000 and 0777")
		}
		config.Umask = value
	}
	return *config, nil
}

func decodeConfigJSON(reader io.Reader, value any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return fmt.Errorf("trailing JSON data: %w", err)
		}
		return fmt.Errorf("unexpected trailing JSON value")
	}
	return nil
}
