package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const maxSyncLogTailBytes int64 = 256 * 1024

type syncLogJSON struct {
	SchemaVersion int    `json:"schemaVersion"`
	Profile       string `json:"profile"`
	Path          string `json:"path"`
	Content       string `json:"content"`
}

func newSyncLogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "log",
		Short:        "Show the tail of the profile sync log",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         runSyncLog,
	}
	cmd.Flags().Int("tail", 200, "maximum number of newest lines")
	cmd.Flags().Bool("json", false, "print a stable machine-readable document")
	return cmd
}

func runSyncLog(cmd *cobra.Command, _ []string) error {
	_, cfg, _, err := syncBootstrapReadOnly(cmd)
	if err != nil {
		return err
	}
	lines, _ := cmd.Flags().GetInt("tail")
	if lines < 1 {
		lines = 1
	}
	if lines > 1000 {
		lines = 1000
	}
	content := tailSyncLog(cfg.LogFile, lines)
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(syncLogJSON{
			SchemaVersion: syncStatusSchemaVersion,
			Profile:       cfg.Profile,
			Path:          cfg.LogFile,
			Content:       content,
		})
	}
	_, err = io.WriteString(cmd.OutOrStdout(), content)
	return err
}

func tailSyncLog(path string, maxLines int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	offset := info.Size() - maxSyncLogTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	body, err := io.ReadAll(file)
	if err != nil {
		return ""
	}
	text := string(body)
	if offset > 0 {
		if _, rest, ok := strings.Cut(text, "\n"); ok {
			text = rest
		}
	}
	rows := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(rows) > maxLines {
		rows = rows[len(rows)-maxLines:]
	}
	if len(rows) == 1 && rows[0] == "" {
		return ""
	}
	return strings.Join(rows, "\n")
}
