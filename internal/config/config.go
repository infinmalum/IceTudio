package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Paths struct {
	Config string
	Data   string
}

// DefaultPaths keeps application state beside the installed executable. It
// never depends on the working directory or falls back to a user directory.
func DefaultPaths() (Paths, error) {
	executable, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("定位应用程序: %w", err)
	}
	// AppImage executes a binary in a read-only temporary mount. APPIMAGE points
	// to the installed image, whose sibling data directory remains persistent.
	if runtime.GOOS == "linux" && os.Getenv("APPIMAGE") != "" {
		executable = os.Getenv("APPIMAGE")
	}
	return pathsForExecutable(executable)
}

func pathsForExecutable(executable string) (Paths, error) {
	if !filepath.IsAbs(executable) {
		return Paths{}, fmt.Errorf("应用程序路径必须是绝对路径: %s", executable)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return Paths{}, fmt.Errorf("解析应用程序位置: %w", err)
	}
	root := filepath.Join(filepath.Dir(resolved), "data")
	return Paths{Config: filepath.Join(root, "config.json"), Data: root}, nil
}

type Config struct {
	Model      Model       `json:"model"`
	MCPServers []MCPServer `json:"mcpServers,omitempty"`
}

// MCPServer describes an explicitly enabled stdio MCP server process.
type MCPServer struct {
	// WebSearchTools explicitly registers this server's web-search tool names.
	WebSearchTools []string `json:"webSearchTools,omitempty"`
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	WorkingDir     string   `json:"workingDir,omitempty"`
	// Env lists environment variable NAMES the server process may inherit
	// from IceTudio. Values are read from the host environment at launch and
	// never stored in the config file; when the list is empty only PATH and
	// HOME are passed, so secrets never reach MCP processes by default.
	Env []string `json:"env,omitempty"`
	// TimeoutSeconds bounds the initial connect/handshake; 0 means the
	// 10-second default.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

func (m MCPServer) validate() error {
	if strings.TrimSpace(m.Name) == "" || strings.Contains(m.Name, "__") || m.Name == "local" {
		return fmt.Errorf("MCP 服务名称无效: %q", m.Name)
	}
	if strings.TrimSpace(m.Command) == "" {
		return fmt.Errorf("MCP 服务 %q 缺少启动命令", m.Name)
	}
	for _, name := range m.WebSearchTools {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name || len(name) > 512 {
			return fmt.Errorf("MCP 服务 %q 的搜索工具名称无效", m.Name)
		}
	}
	for _, name := range m.Env {
		if name == "" || strings.ContainsAny(name, " =") {
			return fmt.Errorf("MCP 服务 %q 的环境允许列表只能包含变量名", m.Name)
		}
	}
	return nil
}

type Model struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	Name     string `json:"name"`
}

// LocalProviders are the key-less OpenAI-compatible local servers.
var LocalProviders = []string{"ollama", "lmstudio"}

// RemoteProviders are the official hosted APIs; they require a separately
// stored credential and all their traffic passes the data exfiltration check.
var RemoteProviders = []string{"openai", "anthropic", "gemini"}

// IsProvider reports whether the provider name is supported at all.
func IsProvider(p string) bool {
	for _, known := range append(append([]string{}, LocalProviders...), RemoteProviders...) {
		if known == p {
			return true
		}
	}
	return false
}

// IsRemoteProvider reports whether the provider is a hosted API that needs a
// credential and the exfiltration check.
func IsRemoteProvider(p string) bool {
	for _, known := range RemoteProviders {
		if known == p {
			return true
		}
	}
	return false
}

// DefaultBaseURL is the official endpoint of a remote provider. An empty
// stored BaseURL means "use this default".
func DefaultBaseURL(provider string) string {
	switch provider {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com"
	case "gemini":
		return "https://generativelanguage.googleapis.com"
	default:
		return ""
	}
}

// EffectiveBaseURL resolves the endpoint a request will actually be sent to:
// the stored override, or the provider's official endpoint. The exfiltration
// check compares against this origin, never against the provider name.
func (m Model) EffectiveBaseURL() string {
	if m.BaseURL != "" {
		return m.BaseURL
	}
	return DefaultBaseURL(m.Provider)
}

func Default() Config {
	return Config{Model: DefaultModel("ollama")}
}

func DefaultModel(provider string) Model {
	switch provider {
	case "openai":
		return Model{Provider: provider, BaseURL: DefaultBaseURL(provider)}
	case "anthropic":
		return Model{Provider: provider, BaseURL: DefaultBaseURL(provider)}
	case "gemini":
		return Model{Provider: provider, BaseURL: DefaultBaseURL(provider)}
	case "lmstudio":
		return Model{Provider: provider, BaseURL: "http://127.0.0.1:1234/v1"}
	default:
		return Model{Provider: "ollama", BaseURL: "http://127.0.0.1:11434/v1"}
	}
}

func (m Model) Validate() error {
	if !IsProvider(m.Provider) {
		return fmt.Errorf("暂不支持模型提供方 %q", m.Provider)
	}
	if IsRemoteProvider(m.Provider) {
		if m.BaseURL == "" {
			return nil
		}
		u, err := url.Parse(m.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || strings.ContainsAny(m.BaseURL, "?#") {
			return errors.New("远端模型地址必须是有效的 http(s) 地址")
		}
		return nil
	}
	u, err := url.Parse(m.BaseURL)
	if err != nil || u.Scheme != "http" || u.User != nil || strings.ContainsAny(m.BaseURL, "?#") || strings.TrimRight(u.Path, "/") != "/v1" {
		return errors.New("模型地址必须是本机 HTTP 服务的 /v1 地址")
	}
	host := u.Hostname()
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return errors.New("模型地址必须指向本机")
	}
	if u.Port() == "" {
		return errors.New("模型地址必须包含端口")
	}
	return nil
}

// Load reads a user-level config. A missing file gets safe local defaults.
func Load(path string) (Config, error) {
	result := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("读取配置: %w", err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		return Config{}, fmt.Errorf("解析配置: %w", err)
	}
	if saved.Model.Provider != "" {
		result.Model = DefaultModel(saved.Model.Provider)
		result.Model.Provider = saved.Model.Provider
	}
	if saved.Model.BaseURL != "" {
		result.Model.BaseURL = saved.Model.BaseURL
	}
	result.Model.Name = saved.Model.Name
	result.MCPServers = saved.MCPServers
	for _, server := range result.MCPServers {
		if err := server.validate(); err != nil {
			return Config{}, err
		}
	}
	if err := result.Model.Validate(); err != nil {
		return Config{}, err
	}
	return result, nil
}

func Save(path string, cfg Config) error {
	if err := cfg.Model.Validate(); err != nil {
		return err
	}
	for _, server := range cfg.MCPServers {
		if err := server.validate(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func EnsureDirs(paths Paths) error {
	for _, dir := range []string{filepath.Dir(paths.Config), paths.Data, filepath.Join(paths.Data, "skills")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("无法创建应用数据目录 %s，请将应用放在可写目录: %w", dir, err)
		}
	}
	return nil
}
