package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// NotificationEventConfig controls delivery for a single event type.
type NotificationEventConfig struct {
	Toast             bool `toml:"toast"`
	Bell              bool `toml:"bell"`
	OsNotify          bool `toml:"os_notify"`
	Popup             bool `toml:"popup"`
	PopupTimeout      int  `toml:"popup_timeout"`
	MinWorkingSeconds int  `toml:"min_working_seconds"`
}

// NotificationConfig controls all notification behavior.
type NotificationConfig struct {
	Enabled      bool                    `toml:"enabled"`
	NeedsInput   NotificationEventConfig `toml:"needs_input"`
	TaskComplete NotificationEventConfig `toml:"task_complete"`
}

// PickerConfig controls picker TUI behavior.
type PickerConfig struct {
	QuickProjectPath    string  `toml:"quick_project_path"`
	PopupWidth          string  `toml:"popup_width"`
	PopupHeight         string  `toml:"popup_height"`
	GridRefreshInterval float64 `toml:"grid_refresh_interval"`
	ShowHelpBar         bool    `toml:"show_help_bar"`
	Mouse               bool    `toml:"mouse"`
	Animations          bool    `toml:"animations"`
	// EnterAction is what Enter does on a session: "focus" opens its terminal
	// inside nagare, "jump" switches to it in tmux as the picker always did.
	EnterAction string `toml:"enter_action"`
	// FocusLeaveKey returns from an agent's terminal to the session list. It is
	// the one key focus mode keeps from the agent that has no alternative, so it
	// is configurable for layouts where Ctrl+] is awkward to type.
	FocusLeaveKey string `toml:"focus_leave_key"`
	// RestoreLayout reopens the focus-mode tiles nagare was closed on.
	RestoreLayout bool `toml:"restore_layout"`
}

// Enter actions.
const (
	EnterFocus = "focus"
	EnterJump  = "jump"
)

// AppearanceConfig controls visual settings.
type AppearanceConfig struct {
	Theme     string `toml:"theme"`
	IconStyle string `toml:"icon_style"`
}

// NagareConfig is the top-level configuration.
type NagareConfig struct {
	Notifications        NotificationConfig `toml:"notifications"`
	Picker               PickerConfig       `toml:"picker"`
	Appearance           AppearanceConfig   `toml:"appearance"`
	NotificationDuration int                `toml:"notification_duration"`
}

// Default returns the default configuration.
func Default() NagareConfig {
	return NagareConfig{
		Notifications: NotificationConfig{
			Enabled: true,
			NeedsInput: NotificationEventConfig{
				Toast:             true,
				Bell:              true,
				OsNotify:          true,
				Popup:             false,
				PopupTimeout:      10,
				MinWorkingSeconds: 0,
			},
			TaskComplete: NotificationEventConfig{
				Toast:             true,
				Bell:              false,
				OsNotify:          false,
				Popup:             false,
				PopupTimeout:      10,
				MinWorkingSeconds: 30,
			},
		},
		Picker: PickerConfig{
			QuickProjectPath:    "~/Prototypes",
			PopupWidth:          "80%",
			PopupHeight:         "80%",
			GridRefreshInterval: 0.5,
			ShowHelpBar:         true,
			Mouse:               true,
			Animations:          true,
			EnterAction:         EnterFocus,
			FocusLeaveKey:       "ctrl+]",
			RestoreLayout:       true,
		},
		Appearance: AppearanceConfig{
			Theme:     "tokyonight",
			IconStyle: "emoji",
		},
		NotificationDuration: 3000,
	}
}

// DefaultPath returns the default config file path.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "nagare", "config.toml")
}

// LoadFrom loads config from a specific path. Returns defaults if file doesn't exist.
// Uses a two-pass approach: first decode into a raw map to see which sections were
// specified, then only override defaults for sections that are present. This prevents
// a partial [notifications.needs_input] from zeroing out unspecified fields.
func LoadFrom(path string) (NagareConfig, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	// First pass: decode into raw map to detect which keys are present
	var raw map[string]interface{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return cfg, err
	}

	// Second pass: decode into a zero-value struct
	var parsed NagareConfig
	if _, err := toml.Decode(string(data), &parsed); err != nil {
		return cfg, err
	}

	// Merge top-level fields
	if notifRaw, ok := raw["notifications"]; ok {
		notifMap, _ := notifRaw.(map[string]interface{})
		if _, ok := notifMap["enabled"]; ok {
			cfg.Notifications.Enabled = parsed.Notifications.Enabled
		}
		if _, ok := notifMap["needs_input"]; ok {
			cfg.Notifications.NeedsInput = mergeEventConfig(cfg.Notifications.NeedsInput, parsed.Notifications.NeedsInput, notifMap["needs_input"])
		}
		if _, ok := notifMap["task_complete"]; ok {
			cfg.Notifications.TaskComplete = mergeEventConfig(cfg.Notifications.TaskComplete, parsed.Notifications.TaskComplete, notifMap["task_complete"])
		}
	}
	if _, ok := raw["picker"]; ok {
		cfg.Picker = mergePicker(cfg.Picker, parsed.Picker, raw["picker"])
	}
	if _, ok := raw["appearance"]; ok {
		cfg.Appearance = mergeAppearance(cfg.Appearance, parsed.Appearance, raw["appearance"])
	}
	if _, ok := raw["notification_duration"]; ok {
		cfg.NotificationDuration = parsed.NotificationDuration
	}

	return cfg, nil
}

// mergeEventConfig merges only the fields that were specified in the TOML.
func mergeEventConfig(defaults, parsed NotificationEventConfig, rawVal interface{}) NotificationEventConfig {
	m, ok := rawVal.(map[string]interface{})
	if !ok {
		return defaults
	}
	result := defaults
	if _, ok := m["toast"]; ok {
		result.Toast = parsed.Toast
	}
	if _, ok := m["bell"]; ok {
		result.Bell = parsed.Bell
	}
	if _, ok := m["os_notify"]; ok {
		result.OsNotify = parsed.OsNotify
	}
	if _, ok := m["popup"]; ok {
		result.Popup = parsed.Popup
	}
	if _, ok := m["popup_timeout"]; ok {
		result.PopupTimeout = parsed.PopupTimeout
	}
	if _, ok := m["min_working_seconds"]; ok {
		result.MinWorkingSeconds = parsed.MinWorkingSeconds
	}
	return result
}

// mergePicker merges only specified picker fields.
func mergePicker(defaults, parsed PickerConfig, rawVal interface{}) PickerConfig {
	m, ok := rawVal.(map[string]interface{})
	if !ok {
		return defaults
	}
	result := defaults
	if _, ok := m["quick_project_path"]; ok {
		result.QuickProjectPath = parsed.QuickProjectPath
	}
	if _, ok := m["popup_width"]; ok {
		result.PopupWidth = parsed.PopupWidth
	}
	if _, ok := m["popup_height"]; ok {
		result.PopupHeight = parsed.PopupHeight
	}
	if _, ok := m["grid_refresh_interval"]; ok {
		result.GridRefreshInterval = parsed.GridRefreshInterval
	}
	if _, ok := m["show_help_bar"]; ok {
		result.ShowHelpBar = parsed.ShowHelpBar
	}
	if _, ok := m["mouse"]; ok {
		result.Mouse = parsed.Mouse
	}
	if _, ok := m["animations"]; ok {
		result.Animations = parsed.Animations
	}
	if _, ok := m["enter_action"]; ok {
		result.EnterAction = parsed.EnterAction
	}
	if _, ok := m["restore_layout"]; ok {
		result.RestoreLayout = parsed.RestoreLayout
	}
	if v, ok := m["focus_leave_key"]; ok && v != "" {
		result.FocusLeaveKey = parsed.FocusLeaveKey
	}
	return result
}

// mergeAppearance merges only specified appearance fields.
func mergeAppearance(defaults, parsed AppearanceConfig, rawVal interface{}) AppearanceConfig {
	m, ok := rawVal.(map[string]interface{})
	if !ok {
		return defaults
	}
	result := defaults
	if _, ok := m["theme"]; ok {
		result.Theme = parsed.Theme
	}
	if _, ok := m["icon_style"]; ok {
		result.IconStyle = parsed.IconStyle
	}
	return result
}

// Load loads config from the default path.
func Load() (NagareConfig, error) {
	return LoadFrom(DefaultPath())
}

// Save writes the config to the default path.
func Save(cfg NagareConfig) error {
	path := DefaultPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var b strings.Builder

	// Write notifications section
	fmt.Fprintf(&b, "[notifications]\nenabled = %t\n\n", cfg.Notifications.Enabled)

	// Write needs_input section
	fmt.Fprintf(&b, "[notifications.needs_input]\n")
	fmt.Fprintf(&b, "toast = %t\n", cfg.Notifications.NeedsInput.Toast)
	fmt.Fprintf(&b, "bell = %t\n", cfg.Notifications.NeedsInput.Bell)
	fmt.Fprintf(&b, "os_notify = %t\n", cfg.Notifications.NeedsInput.OsNotify)
	fmt.Fprintf(&b, "popup = %t\n", cfg.Notifications.NeedsInput.Popup)
	fmt.Fprintf(&b, "popup_timeout = %d\n", cfg.Notifications.NeedsInput.PopupTimeout)
	fmt.Fprintf(&b, "min_working_seconds = %d\n\n", cfg.Notifications.NeedsInput.MinWorkingSeconds)

	// Write task_complete section
	fmt.Fprintf(&b, "[notifications.task_complete]\n")
	fmt.Fprintf(&b, "toast = %t\n", cfg.Notifications.TaskComplete.Toast)
	fmt.Fprintf(&b, "bell = %t\n", cfg.Notifications.TaskComplete.Bell)
	fmt.Fprintf(&b, "os_notify = %t\n", cfg.Notifications.TaskComplete.OsNotify)
	fmt.Fprintf(&b, "popup = %t\n", cfg.Notifications.TaskComplete.Popup)
	fmt.Fprintf(&b, "popup_timeout = %d\n", cfg.Notifications.TaskComplete.PopupTimeout)
	fmt.Fprintf(&b, "min_working_seconds = %d\n\n", cfg.Notifications.TaskComplete.MinWorkingSeconds)

	// Write picker section
	fmt.Fprintf(&b, "[picker]\n")
	fmt.Fprintf(&b, "quick_project_path = %q\n", cfg.Picker.QuickProjectPath)
	fmt.Fprintf(&b, "popup_width = %q\n", cfg.Picker.PopupWidth)
	fmt.Fprintf(&b, "popup_height = %q\n", cfg.Picker.PopupHeight)
	fmt.Fprintf(&b, "grid_refresh_interval = %f\n", cfg.Picker.GridRefreshInterval)
	fmt.Fprintf(&b, "show_help_bar = %t\n\n", cfg.Picker.ShowHelpBar)

	// Write appearance section
	fmt.Fprintf(&b, "[appearance]\n")
	fmt.Fprintf(&b, "theme = %q\n", cfg.Appearance.Theme)
	fmt.Fprintf(&b, "icon_style = %q\n\n", cfg.Appearance.IconStyle)

	// Write notification_duration
	fmt.Fprintf(&b, "notification_duration = %d\n", cfg.NotificationDuration)

	return os.WriteFile(path, []byte(b.String()), 0644)
}
