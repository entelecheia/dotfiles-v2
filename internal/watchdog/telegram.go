package watchdog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadTelegramEnv reads the bot credentials from the secrets-managed env
// file at path (KEY=VALUE lines, '#' comments, an optional "export " prefix
// and surrounding quotes tolerated). A missing file means the channel was
// never configured: ok=false, not an error. Process environment variables
// TELEGRAM_BOT_TOKEN / TELEGRAM_CHAT_ID override file values, so an operator
// can rotate a credential for one run without editing the file (the env-wins
// precedence the beszel unit establishes). ok reports whether both
// credentials resolved to a non-empty value.
func LoadTelegramEnv(path string) (token, chatID string, ok bool, err error) {
	values := map[string]string{}
	data, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		parsed, perr := scanEnvLines(string(data))
		if perr != nil {
			return "", "", false, fmt.Errorf("parsing %s: %w", path, perr)
		}
		values = parsed
	case os.IsNotExist(readErr):
		// Not configured; the environment may still carry the credentials.
	default:
		return "", "", false, fmt.Errorf("reading telegram env %s: %w", path, readErr)
	}
	token = values["TELEGRAM_BOT_TOKEN"]
	chatID = values["TELEGRAM_CHAT_ID"]
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		token = v
	}
	if v := os.Getenv("TELEGRAM_CHAT_ID"); v != "" {
		chatID = v
	}
	return token, chatID, token != "" && chatID != "", nil
}

// parseTelegramEnvLine parses one env-file line. keep is false for blank
// lines and comments.
func parseTelegramEnvLine(line string) (key, value string, keep bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	k, v, found := strings.Cut(line, "=")
	if !found {
		return "", "", false, fmt.Errorf("expected KEY=VALUE, got %q", line)
	}
	key = strings.TrimSpace(k)
	if key == "" {
		return "", "", false, fmt.Errorf("empty key in %q", line)
	}
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if q := v[0]; (q == '"' || q == '\'') && v[len(v)-1] == q {
			v = v[1 : len(v)-1]
		}
	}
	return key, v, true, nil
}

// telegramMessage is the Bot API sendMessage payload. ChatID is any because
// the API takes an integer chat id or a string (@channelname); a numeric
// string from the env file is sent as a number.
type telegramMessage struct {
	ChatID                any    `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// notifyTelegram delivers one alert through the Telegram Bot API. The env
// file is read lazily at send time so a scheduled run picks up a rotated
// credential without re-running setup. Enabled but unconfigured (no
// credentials in the file or the environment) is a clean no-op — the channel
// cannot mistake an unconfigured host for a failed send.
func (n *Notifier) notifyTelegram(ctx context.Context, level, msg string) error {
	settings := n.Settings.Telegram
	token, chatID, ok, err := LoadTelegramEnv(settings.EnvPath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	base := settings.APIBase
	if base == "" {
		base = DefaultTelegramAPIBase
	}
	client := n.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	text := msg
	if level != "" && level != "info" {
		text = "[" + level + "] " + msg
	}
	body, err := json.Marshal(telegramMessage{
		ChatID:                telegramChatID(chatID),
		Text:                  text,
		DisableWebPagePreview: true,
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(base, "/") + "/bot" + token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram sendMessage request: %s", redactTelegramToken(err, token))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// url.Error echoes the request URL, which carries the bot token;
		// callers log this error, so it must never contain the secret.
		return fmt.Errorf("telegram sendMessage: %s", redactTelegramToken(err, token))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram returned %s", resp.Status)
	}
	return nil
}

// telegramChatID sends a purely numeric chat id (negative group ids
// included) as a JSON number, everything else as a string.
func telegramChatID(chatID string) any {
	if id, err := strconv.ParseInt(chatID, 10, 64); err == nil {
		return id
	}
	return chatID
}

// redactTelegramToken strips the bot token from an error's text so the
// wrapped error is safe to log.
func redactTelegramToken(err error, token string) string {
	if token == "" {
		return err.Error()
	}
	return strings.ReplaceAll(err.Error(), token, "<redacted>")
}

// scanEnvLines parses env-file content into a KEY→VALUE map, reporting the
// offending line on a malformed entry.
func scanEnvLines(content string) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		key, value, keep, err := parseTelegramEnvLine(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if keep {
			values[key] = value
		}
	}
	return values, scanner.Err()
}
