package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type Config struct {
	Server         string `json:"server"`
	Expiry         string `json:"expiry"`
	DownloadDir    string `json:"download_dir"`
	LanPort        int    `json:"lan_port"`
	AdaptiveChunks bool   `json:"adaptive_chunks"`
	ShowQR         string `json:"show_qr"`
}

func defaultConfig() *Config {
	return &Config{
		Server:         "https://uplink-delta-xi.vercel.app",
		Expiry:         "1h",
		DownloadDir:    "", // empty means current directory or system default
		LanPort:        9090,
		AdaptiveChunks: true,
		ShowQR:         "auto",
	}
}

func getConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".uplink", "config.json")
}

func LoadConfig() *Config {
	cfg := defaultConfig()

	// 1. Env vars override defaults
	if s := os.Getenv("UPLINK_SERVER"); s != "" {
		cfg.Server = s
	}
	if e := os.Getenv("UPLINK_EXPIRY"); e != "" {
		cfg.Expiry = e
	}
	if d := os.Getenv("UPLINK_DOWNLOAD_DIR"); d != "" {
		cfg.DownloadDir = d
	}
	if p := os.Getenv("UPLINK_LAN_PORT"); p != "" {
		if val, err := strconv.Atoi(p); err == nil {
			cfg.LanPort = val
		}
	}
	if a := os.Getenv("UPLINK_ADAPTIVE_CHUNKS"); a != "" {
		if val, err := strconv.ParseBool(a); err == nil {
			cfg.AdaptiveChunks = val
		}
	}
	if q := os.Getenv("UPLINK_SHOW_QR"); q != "" {
		cfg.ShowQR = q
	}

	// 2. Config file overrides defaults/env
	path := getConfigPath()
	if data, err := os.ReadFile(path); err == nil {
		var fileCfg Config
		if json.Unmarshal(data, &fileCfg) == nil {
			if fileCfg.Server != "" {
				cfg.Server = fileCfg.Server
			}
			if fileCfg.Expiry != "" {
				cfg.Expiry = fileCfg.Expiry
			}
			if fileCfg.DownloadDir != "" {
				cfg.DownloadDir = fileCfg.DownloadDir
			}
			if fileCfg.LanPort != 0 {
				cfg.LanPort = fileCfg.LanPort
			}
			// For boolean fields we can check if it exists or override unconditionally
			cfg.AdaptiveChunks = fileCfg.AdaptiveChunks
			if fileCfg.ShowQR != "" {
				cfg.ShowQR = fileCfg.ShowQR
			}
		}
	}

	return cfg
}

func saveConfig(cfg *Config) error {
	path := getConfigPath()
	dir := filepath.Dir(path)
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	err = enc.Encode(cfg)
	if err != nil {
		return err
	}
	f.Close()
	return os.Rename(tmp, path)
}

func handleConfigSubcommand(args []string) {
	if len(args) == 0 {
		// Print current config
		cfg := LoadConfig()
		data, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(data))
		return
	}

	sub := args[0]
	switch sub {
	case "set":
		if len(args) < 3 {
			fmt.Println("Usage: uplink config set <key> <value>")
			os.Exit(1)
		}
		key := args[1]
		value := args[2]
		err := setConfigKey(key, value)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Config updated: %s = %s\n", key, value)
	case "reset":
		path := getConfigPath()
		_ = os.Remove(path)
		fmt.Println("Config reset to defaults.")
	default:
		fmt.Printf("Unknown config subcommand: %s\n", sub)
		fmt.Println("Usage:\n  uplink config\n  uplink config set <key> <value>\n  uplink config reset")
		os.Exit(1)
	}
}

func setConfigKey(key, value string) error {
	cfg := LoadConfig()
	val := reflect.ValueOf(cfg).Elem()

	// Map snake_case key to camelCase struct fields
	camelKey := ""
	parts := strings.Split(key, "_")
	for _, p := range parts {
		if len(p) > 0 {
			camelKey += strings.ToUpper(p[:1]) + p[1:]
		}
	}
	// Exceptional casing
	if camelKey == "ShowQr" {
		camelKey = "ShowQR"
	}

	field := val.FieldByName(camelKey)
	if !field.IsValid() {
		return fmt.Errorf("unknown config key: %s", key)
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(value)
	case reflect.Int:
		intVal, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid integer: %s", value)
		}
		field.SetInt(int64(intVal))
	case reflect.Bool:
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean: %s", value)
		}
		field.SetBool(boolVal)
	default:
		return fmt.Errorf("unsupported config field type for key %s", key)
	}

	return saveConfig(cfg)
}
