package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/syncer"
)

const peerHomePathsSchemaVersion = 1

type peerHomePathsJSON struct {
	SchemaVersion int    `json:"schemaVersion"`
	Path          string `json:"path"`
	ActiveCount   int    `json:"activeCount"`
	Content       string `json:"content"`
}

func newPeerHomePathsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "home-paths",
		Short:        "Read or replace the peer host-path allowlist",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	get := &cobra.Command{
		Use:          "get",
		Short:        "Read the peer host-path list",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         runPeerHomePathsGet,
	}
	get.Flags().Bool("json", false, "print a stable machine-readable document")
	set := &cobra.Command{
		Use:          "set",
		Short:        "Replace the peer host-path list from stdin",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         runPeerHomePathsSet,
	}
	set.Flags().Bool("json", false, "print the updated document")
	cmd.AddCommand(get, set)
	return cmd
}

func readPeerHomePaths(path string) (peerHomePathsJSON, error) {
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return peerHomePathsJSON{}, err
	}
	content := string(body)
	return peerHomePathsJSON{
		SchemaVersion: peerHomePathsSchemaVersion,
		Path:          path,
		ActiveCount:   activePatternCount(content),
		Content:       content,
	}, nil
}

func runPeerHomePathsGet(cmd *cobra.Command, _ []string) error {
	bs, err := peerBootstrapReadOnly()
	if err != nil {
		return err
	}
	document, err := readPeerHomePaths(peerHomePathsFile(bs.Config.LocalPaths))
	if err != nil {
		return err
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(document)
	}
	_, err = io.WriteString(cmd.OutOrStdout(), document.Content)
	return err
}

func runPeerHomePathsSet(cmd *cobra.Command, _ []string) error {
	bs, err := syncer.Bootstrap(peerBootstrapOptions(cmd))
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxPatternFileBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxPatternFileBytes {
		return fmt.Errorf("peer home-path list exceeds %d bytes", maxPatternFileBytes)
	}
	content := strings.ReplaceAll(string(body), "\r\n", "\n")
	if strings.ContainsRune(content, '\x00') {
		return fmt.Errorf("peer home-path list contains a NUL byte")
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	path := peerHomePathsFile(bs.Config.LocalPaths)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if !dryRun {
		if err := writePatternFileAtomic(path, []byte(content)); err != nil {
			return err
		}
	}
	document := peerHomePathsJSON{
		SchemaVersion: peerHomePathsSchemaVersion,
		Path:          path,
		ActiveCount:   activePatternCount(content),
		Content:       content,
	}
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(document)
	}
	printerFrom(cmd).Success("peer host paths updated")
	return nil
}
