package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entelecheia/dotfiles-v2/internal/config"
	"github.com/entelecheia/dotfiles-v2/internal/exec"
	"github.com/entelecheia/dotfiles-v2/internal/fileutil"
	"github.com/entelecheia/dotfiles-v2/internal/template"
	"github.com/entelecheia/dotfiles-v2/internal/tunnel"
	"github.com/entelecheia/dotfiles-v2/internal/ui"
)

func newTunnelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage Cloudflare Tunnel SSH access for this Mac",
		Long: `Configure a locally managed Cloudflare Tunnel for SSH access to this Mac.

Server commands are macOS-only and install a system LaunchDaemon. Client
commands only manage ~/.ssh/config.d/dot-tunnel and work cross-platform.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SilenceUsage: true,
	}
	cmd.AddCommand(
		newTunnelSetupCmd(),
		newTunnelStatusCmd(),
		newTunnelLogCmd(),
		newTunnelUninstallCmd(),
		newTunnelClientCmd(),
	)
	return cmd
}

func newTunnelSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "setup",
		Short:        "Configure this Mac for SSH over Cloudflare Tunnel",
		Args:         cobra.NoArgs,
		RunE:         func(cmd *cobra.Command, args []string) error { return runTunnelSetupForGOOS(cmd, args, runtime.GOOS) },
		SilenceUsage: true,
	}
	cmd.Flags().String("hostname", "", "public SSH hostname (required with --yes on first setup)")
	cmd.Flags().String("name", "", "tunnel name (default: dot-<short-hostname>)")
	return cmd
}

func runTunnelSetupForGOOS(cmd *cobra.Command, _ []string, goos string) error {
	if goos != "darwin" {
		return fmt.Errorf("dot tunnel setup is macOS-only; use 'dot tunnel client' on other platforms")
	}
	if err := rejectTunnelHomeOverride(cmd); err != nil {
		return err
	}

	ctx := context.Background()
	p := printerFrom(cmd)
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	runner := tunnelRunner(dryRun)
	engine := template.NewEngine()

	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	p.Header("dot tunnel setup")

	// Resolve and validate all inputs before any mutation (brew install,
	// browser login, sudo) so an unattended run with missing inputs fails
	// immediately instead of midway through system changes.
	tunnelName, hostname, err := resolveTunnelInputs(cmd, state, yes)
	if err != nil {
		return err
	}
	if err := tunnel.ValidateTunnelName(tunnelName); err != nil {
		return err
	}
	if err := tunnel.ValidateHostname(hostname); err != nil {
		return err
	}

	cloudflaredPath, err := ensureCloudflared(ctx, p, runner, yes, dryRun)
	if err != nil {
		return err
	}
	if err := ensureCloudflaredCert(ctx, p, runner, cloudflaredPath, home, dryRun); err != nil {
		return err
	}

	if err := ensureRemoteLogin(ctx, p, runner, yes, dryRun); err != nil {
		return err
	}

	if dryRun {
		printTunnelSetupDryRun(p, cloudflaredPath, tunnelName, hostname)
		return nil
	}

	record, credSrc, err := resolveTunnelRecord(ctx, p, runner, cloudflaredPath, home, tunnelName)
	if err != nil {
		return err
	}
	if err := tunnel.ValidateTunnelID(record.ID); err != nil {
		return err
	}

	p.Line("Priming sudo for /etc/cloudflared and LaunchDaemon installation...")
	if err := runner.RunInteractive(ctx, "sudo", "-v"); err != nil {
		return err
	}

	if err := installTunnelConfig(ctx, p, runner, engine, record.ID, hostname, credSrc); err != nil {
		return err
	}
	if err := routeTunnelDNS(ctx, p, runner, cloudflaredPath, tunnelName, hostname, yes); err != nil {
		return err
	}
	if err := installTunnelDaemon(ctx, p, runner, engine, cloudflaredPath); err != nil {
		return err
	}

	if connections, err := tunnel.WaitForConnections(ctx, runner, cloudflaredPath, tunnelName, 20*time.Second); err == nil {
		p.Line("  %s tunnel connector registered (%d connection(s))", ui.StyleSuccess.Render("✓"), connections)
	} else {
		p.Warn("tunnel daemon installed, but no active connector was observed within 20s")
		p.Warn("check logs with: dot tunnel log 100")
	}

	state.Modules.Tunnel = config.UserTunnelState{
		TunnelName: tunnelName,
		TunnelID:   record.ID,
		Hostname:   hostname,
	}
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving tunnel state: %w", err)
	}

	p.Success("✓ dot tunnel setup complete")
	printAccessGuide(p, hostname)
	return nil
}

func newTunnelStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "status",
		Short:        "Show Cloudflare Tunnel daemon and SSH status",
		Args:         cobra.NoArgs,
		RunE:         runTunnelStatus,
		SilenceUsage: true,
	}
}

func runTunnelStatus(cmd *cobra.Command, _ []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("dot tunnel status is macOS-only; use 'dot tunnel client' on other platforms")
	}
	if err := rejectTunnelHomeOverride(cmd); err != nil {
		return err
	}

	ctx := context.Background()
	p := printerFrom(cmd)
	runner := tunnelRunner(false)
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	p.Header("dot tunnel status")
	cloudflaredPath, found := lookupCloudflared()
	if found {
		p.KV("cloudflared", cloudflaredVersion(ctx, runner, cloudflaredPath))
	} else {
		p.KV("cloudflared", "not found")
	}
	p.KV("Tunnel", tunnelStateLabel(state.Modules.Tunnel))
	p.KV("Hostname", state.Modules.Tunnel.Hostname)
	p.KV("Config", filePresence(tunnel.ConfigPath))
	p.KV("Daemon", string(tunnel.DaemonStateFor(ctx, runner)))
	if tunnel.Port22Open(time.Second) {
		p.KV("Port 22", "open")
	} else {
		p.KV("Port 22", "closed")
	}

	connections := "(offline or unauthorized)"
	if found && state.Modules.Tunnel.TunnelName != "" {
		// The connector probe is a Cloudflare API call — bound it so an
		// offline machine doesn't make a local status command hang.
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		record, ok, err := tunnel.LookupTunnelID(lookupCtx, runner, cloudflaredPath, state.Modules.Tunnel.TunnelName)
		cancel()
		if err == nil && ok {
			connections = strconv.Itoa(record.Connections)
		}
	}
	p.KV("Connectors", connections)
	p.Blank()
	p.Line("  Stop:  sudo launchctl bootout system/%s", tunnel.Label)
	p.Line("  Start: sudo launchctl bootstrap system %s", tunnel.PlistPath)
	return nil
}

func newTunnelLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "log [N]",
		Short:        "Tail the Cloudflare Tunnel daemon error log",
		Args:         cobra.MaximumNArgs(1),
		RunE:         runTunnelLog,
		SilenceUsage: true,
	}
}

func runTunnelLog(cmd *cobra.Command, args []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("dot tunnel log is macOS-only")
	}
	if err := rejectTunnelHomeOverride(cmd); err != nil {
		return err
	}
	n := 50
	if len(args) > 0 {
		parsed, err := strconv.Atoi(args[0])
		if err != nil || parsed <= 0 {
			return fmt.Errorf("log line count must be a positive integer")
		}
		n = parsed
	}
	p := printerFrom(cmd)
	lines, err := fileutil.TailLog(tunnel.LogErrPath, n)
	if err != nil {
		p.Line("No log file found at %s", tunnel.LogErrPath)
		return nil
	}
	p.Line("%s", lines)
	return nil
}

func newTunnelUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the dot-managed Cloudflare Tunnel daemon",
		Long: `Remove the dot-managed Cloudflare Tunnel LaunchDaemon.

Removing /etc/cloudflared config/credentials and deleting the Cloudflare
tunnel itself are interactive-only prompts that default to No; --yes never
auto-confirms them.`,
		Args:         cobra.NoArgs,
		RunE:         runTunnelUninstall,
		SilenceUsage: true,
	}
}

func runTunnelUninstall(cmd *cobra.Command, _ []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("dot tunnel uninstall is macOS-only")
	}
	if err := rejectTunnelHomeOverride(cmd); err != nil {
		return err
	}
	ctx := context.Background()
	p := printerFrom(cmd)
	yes, _ := cmd.Flags().GetBool("yes")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	runner := tunnelRunner(dryRun)

	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	if dryRun {
		p.Line("[dry-run] would bootout %s and remove %s", tunnel.Label, tunnel.PlistPath)
		p.Line("[dry-run] would optionally remove %s and credentials, delete the Cloudflare tunnel, and clear state", tunnel.ConfigDir)
		return nil
	}

	_, _ = runner.Run(ctx, "sudo", "launchctl", "bootout", "system/"+tunnel.Label)
	_, _ = runner.Run(ctx, "sudo", "rm", "-f", tunnel.PlistPath)
	p.Line("Removed LaunchDaemon plist (if present).")

	// Destructive extras default to No and are never auto-confirmed by
	// --yes: unattended uninstall must not delete credentials or the
	// remote tunnel (other hosts/clients may still route through it).
	removeConfig, err := ui.ConfirmBool("Remove /etc/cloudflared config and credentials managed by dot?", false, yes)
	if err != nil {
		return err
	}
	if removeConfig {
		_, _ = runner.Run(ctx, "sudo", "rm", "-f", tunnel.ConfigPath)
		if state.Modules.Tunnel.TunnelID != "" {
			_, _ = runner.Run(ctx, "sudo", "rm", "-f", tunnel.EtcCredentialPath(state.Modules.Tunnel.TunnelID))
		}
		_, _ = runner.Run(ctx, "sudo", "rmdir", tunnel.ConfigDir)
		p.Line("Removed /etc/cloudflared managed files (if present).")
	}

	deleteTunnel, err := ui.ConfirmBool("Delete the Cloudflare tunnel itself?", false, yes)
	if err != nil {
		return err
	}
	if deleteTunnel {
		cloudflaredPath, found := lookupCloudflared()
		if !found {
			p.Warn("cloudflared not found; skipping remote tunnel delete")
		} else if state.Modules.Tunnel.TunnelName == "" {
			p.Warn("no tunnel name in state; skipping remote tunnel delete")
		} else if err := runner.RunInteractive(ctx, cloudflaredPath, "tunnel", "delete", state.Modules.Tunnel.TunnelName); err != nil {
			return err
		}
	}

	state.Modules.Tunnel = config.UserTunnelState{}
	if err := config.SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	p.Success("✓ dot tunnel uninstalled")
	p.Line("Remote Login was not changed.")
	p.Line("Remove DNS CNAME records and the Cloudflare Access app manually in the Cloudflare dashboard.")
	return nil
}

func newTunnelClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "client",
		Short:        "Manage SSH client config for Cloudflare Access",
		Args:         cobra.NoArgs,
		RunE:         func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
		SilenceUsage: true,
	}
	cmd.AddCommand(newTunnelClientAddCmd(), newTunnelClientListCmd(), newTunnelClientRemoveCmd())
	return cmd
}

func newTunnelClientAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "add <hostname>",
		Short:        "Add an SSH ProxyCommand host block",
		Args:         cobra.ExactArgs(1),
		RunE:         runTunnelClientAdd,
		SilenceUsage: true,
	}
}

func runTunnelClientAdd(cmd *cobra.Command, args []string) error {
	hostname := strings.ToLower(strings.TrimSpace(args[0]))
	if err := tunnel.ValidateHostname(hostname); err != nil {
		return err
	}
	p := printerFrom(cmd)
	home := homeFromCmd(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		p.Line("[dry-run] would add Host %s to %s", hostname, tunnel.DropInPath(home))
		return nil
	}
	added, warnings, err := tunnel.AddHost(home, hostname)
	if err != nil {
		return err
	}
	if added {
		p.Success("✓ added %s", hostname)
	} else {
		p.Line("%s is already configured; existing block left untouched.", hostname)
	}
	for _, warning := range warnings {
		p.Warn("%s", warning)
	}
	return nil
}

func newTunnelClientListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List SSH hosts managed by dot tunnel client",
		Args:         cobra.NoArgs,
		RunE:         runTunnelClientList,
		SilenceUsage: true,
	}
}

func runTunnelClientList(cmd *cobra.Command, _ []string) error {
	p := printerFrom(cmd)
	hosts, err := tunnel.ListHosts(homeFromCmd(cmd))
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		p.Line("No tunnel SSH hosts configured.")
		return nil
	}
	for _, host := range hosts {
		p.Line("%s", host)
	}
	return nil
}

func newTunnelClientRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "remove <hostname>",
		Short:        "Remove an SSH ProxyCommand host block",
		Args:         cobra.ExactArgs(1),
		RunE:         runTunnelClientRemove,
		SilenceUsage: true,
	}
}

func runTunnelClientRemove(cmd *cobra.Command, args []string) error {
	hostname := strings.ToLower(strings.TrimSpace(args[0]))
	if err := tunnel.ValidateHostname(hostname); err != nil {
		return err
	}
	p := printerFrom(cmd)
	home := homeFromCmd(cmd)
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	if dryRun {
		p.Line("[dry-run] would remove Host %s from %s", hostname, tunnel.DropInPath(home))
		return nil
	}
	removed, err := tunnel.RemoveHost(home, hostname)
	if err != nil {
		return err
	}
	if removed {
		p.Success("✓ removed %s", hostname)
	} else {
		p.Line("%s was not configured.", hostname)
	}
	return nil
}

func ensureCloudflared(ctx context.Context, p *Printer, runner *exec.Runner, yes, dryRun bool) (string, error) {
	if path, found := lookupCloudflared(); found {
		p.Line("  %s cloudflared found at %s", ui.StyleSuccess.Render("✓"), path)
		return path, nil
	}
	if dryRun {
		p.Line("  ~ cloudflared not found; would install with: brew install cloudflared")
		return "cloudflared", nil
	}
	confirmed, err := ui.Confirm("cloudflared not found. Install it with Homebrew?", yes)
	if err != nil {
		return "", err
	}
	if !confirmed {
		return "", fmt.Errorf("cloudflared is required")
	}
	if _, found := lookupBrew(); !found {
		return "", fmt.Errorf("brew not found; install Homebrew first or install cloudflared manually")
	}
	if err := runner.RunAttached(ctx, "brew", "install", "cloudflared"); err != nil {
		return "", err
	}
	if path, found := lookupCloudflared(); found {
		p.Line("  %s cloudflared installed at %s", ui.StyleSuccess.Render("✓"), path)
		return path, nil
	}
	return "", fmt.Errorf("cloudflared not found in PATH after install")
}

func ensureCloudflaredCert(ctx context.Context, p *Printer, runner *exec.Runner, cloudflaredPath, home string, dryRun bool) error {
	cert := tunnel.CertPath(home)
	if _, err := os.Stat(cert); err == nil {
		p.Line("  %s Cloudflare cert found at %s", ui.StyleSuccess.Render("✓"), cert)
		return nil
	}
	p.Line("Cloudflare login cert is missing.")
	p.Line("Choose the zone that will contain the SSH hostname, then complete the browser login.")
	if dryRun {
		p.Line("  ~ would run: %s tunnel login", cloudflaredPath)
		return nil
	}
	if err := runner.RunInteractive(ctx, cloudflaredPath, "tunnel", "login"); err != nil {
		return err
	}
	if _, err := os.Stat(cert); err != nil {
		return fmt.Errorf("cloudflare cert still missing at %s", cert)
	}
	return nil
}

// resolveTunnelInputs resolves the tunnel name and hostname from flags,
// saved state, and (interactively) prompts — in that precedence order.
func resolveTunnelInputs(cmd *cobra.Command, state *config.UserState, yes bool) (string, string, error) {
	flagName, _ := cmd.Flags().GetString("name")
	flagHostname, _ := cmd.Flags().GetString("hostname")

	tunnelName := strings.TrimSpace(flagName)
	if tunnelName == "" {
		defaultName := state.Modules.Tunnel.TunnelName
		if defaultName == "" {
			host, _ := os.Hostname()
			short := strings.Split(host, ".")[0]
			defaultName = "dot-" + strings.ToLower(short)
		}
		input, err := ui.Input("Tunnel name", defaultName, yes)
		if err != nil {
			return "", "", err
		}
		tunnelName = strings.TrimSpace(input)
	}

	hostname := strings.TrimSpace(flagHostname)
	if hostname == "" {
		input, err := ui.Input("SSH hostname (for example mac.example.com)", state.Modules.Tunnel.Hostname, yes)
		if err != nil {
			return "", "", err
		}
		hostname = strings.TrimSpace(input)
	}
	if hostname == "" {
		return "", "", fmt.Errorf("SSH hostname is required; pass --hostname (unattended) or run without --yes")
	}
	return tunnelName, strings.ToLower(hostname), nil
}

func ensureRemoteLogin(ctx context.Context, p *Printer, runner *exec.Runner, yes, dryRun bool) error {
	if tunnel.Port22Open(time.Second) {
		p.Line("  %s Remote Login is reachable on localhost:22", ui.StyleSuccess.Render("✓"))
		return nil
	}
	if dryRun {
		p.Line("  ~ port 22 is closed; would enable Remote Login with systemsetup, then launchctl fallback if needed")
		return nil
	}
	confirmed, err := ui.Confirm("Remote Login is off. Enable SSH on this Mac?", yes)
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("port 22 is closed; tunnel setup aborted")
	}
	if err := runner.RunAttached(ctx, "sudo", "systemsetup", "-setremotelogin", "on"); err != nil {
		return err
	}
	if tunnel.Port22Open(time.Second) {
		p.Line("  %s Remote Login enabled", ui.StyleSuccess.Render("✓"))
		return nil
	}
	p.Warn("port 22 still closed; trying launchctl fallback")
	if err := runner.RunAttached(ctx, "sudo", "launchctl", "load", "-w", "/System/Library/LaunchDaemons/ssh.plist"); err != nil {
		return err
	}
	if tunnel.Port22Open(time.Second) {
		p.Line("  %s Remote Login enabled via launchctl", ui.StyleSuccess.Render("✓"))
		return nil
	}
	return fmt.Errorf("port 22 is still closed. Open System Settings > General > Sharing > Remote Login, then rerun dot tunnel setup")
}

func printTunnelSetupDryRun(p *Printer, cloudflaredPath, tunnelName, hostname string) {
	p.Section("Dry run")
	p.Line("  would look up or create tunnel: %s", tunnelName)
	p.Line("  would install credentials to: %s/<tunnel-id>.json", tunnel.ConfigDir)
	p.Line("  would render config to: %s", tunnel.ConfigPath)
	p.Line("  would route DNS: %s tunnel route dns %s %s", cloudflaredPath, tunnelName, hostname)
	p.Line("  would install LaunchDaemon: %s", tunnel.PlistPath)
	p.Line("  plist command: %s --no-autoupdate --config %s tunnel run", cloudflaredPath, tunnel.ConfigPath)
}

func resolveTunnelRecord(ctx context.Context, p *Printer, runner *exec.Runner, cloudflaredPath, home, tunnelName string) (*tunnel.TunnelRecord, string, error) {
	record, found, err := tunnel.LookupTunnelID(ctx, runner, cloudflaredPath, tunnelName)
	if err != nil {
		return nil, "", err
	}
	if found {
		p.Line("  %s reusing tunnel %s (%s)", ui.StyleSuccess.Render("✓"), tunnelName, record.ID)
		credSrc := firstExistingPath(tunnel.HomeCredentialPath(home, record.ID), tunnel.EtcCredentialPath(record.ID))
		if credSrc == "" {
			return nil, "", fmt.Errorf("credentials JSON missing for tunnel %s. Run: cloudflared tunnel token --cred-file %s %s", record.ID, tunnel.HomeCredentialPath(home, record.ID), record.ID)
		}
		return record, credSrc, nil
	}
	p.Line("Creating tunnel %s...", tunnelName)
	record, err = tunnel.CreateTunnel(ctx, runner, cloudflaredPath, tunnelName)
	if err != nil {
		return nil, "", err
	}
	credSrc := record.CredentialsFile
	if credSrc == "" {
		credSrc = tunnel.HomeCredentialPath(home, record.ID)
	}
	if _, err := os.Stat(credSrc); err != nil {
		return nil, "", fmt.Errorf("credentials JSON missing after tunnel create: %s", credSrc)
	}
	p.Line("  %s created tunnel %s (%s)", ui.StyleSuccess.Render("✓"), tunnelName, record.ID)
	return record, credSrc, nil
}

func installTunnelConfig(ctx context.Context, p *Printer, runner *exec.Runner, engine *template.Engine, tunnelID, hostname, credSrc string) error {
	if _, err := runner.Run(ctx, "sudo", "install", "-d", "-m", "0755", "-o", "root", "-g", "wheel", tunnel.ConfigDir); err != nil {
		return fmt.Errorf("creating %s: %w", tunnel.ConfigDir, err)
	}
	credDest := tunnel.EtcCredentialPath(tunnelID)
	if filepath.Clean(credSrc) != filepath.Clean(credDest) {
		if err := tunnel.SudoInstallFile(ctx, runner, credSrc, credDest, 0o600); err != nil {
			return err
		}
		p.Line("  %s credentials installed to %s", ui.StyleSuccess.Render("✓"), credDest)
	} else {
		p.Line("  %s credentials already installed at %s", ui.StyleSuccess.Render("✓"), credDest)
	}

	cfg, err := tunnel.RenderConfig(engine, tunnelID, hostname)
	if err != nil {
		return err
	}
	if err := tunnel.SudoInstallContent(ctx, runner, cfg, tunnel.ConfigPath, 0o644); err != nil {
		return err
	}
	p.Line("  %s config installed to %s", ui.StyleSuccess.Render("✓"), tunnel.ConfigPath)
	return nil
}

func routeTunnelDNS(ctx context.Context, p *Printer, runner *exec.Runner, cloudflaredPath, tunnelName, hostname string, yes bool) error {
	status, err := tunnel.RouteDNS(ctx, runner, cloudflaredPath, tunnelName, hostname, false)
	if err == nil {
		if status == tunnel.RouteDNSUnchanged {
			p.Line("  %s DNS route already configured", ui.StyleSuccess.Render("✓"))
		} else {
			p.Line("  %s DNS route configured for %s", ui.StyleSuccess.Render("✓"), hostname)
		}
		return nil
	}
	if tunnel.IsZoneMismatchError(err) {
		return fmt.Errorf("DNS route failed: cert.pem is zone-scoped. Re-run 'cloudflared tunnel login' for the zone that owns %s", hostname)
	}
	if !tunnel.IsDNSConflictError(err) {
		return err
	}
	confirmed, confirmErr := ui.Confirm("A DNS record already exists for this hostname. Overwrite it?", yes)
	if confirmErr != nil {
		return confirmErr
	}
	if !confirmed {
		return fmt.Errorf("DNS route conflict for %s", hostname)
	}
	if _, err := tunnel.RouteDNS(ctx, runner, cloudflaredPath, tunnelName, hostname, true); err != nil {
		return err
	}
	p.Line("  %s DNS route overwritten for %s", ui.StyleSuccess.Render("✓"), hostname)
	return nil
}

func installTunnelDaemon(ctx context.Context, p *Printer, runner *exec.Runner, engine *template.Engine, cloudflaredPath string) error {
	plist, err := tunnel.RenderPlist(engine, cloudflaredPath)
	if err != nil {
		return err
	}
	if err := tunnel.SudoInstallContent(ctx, runner, plist, tunnel.PlistPath, 0o644); err != nil {
		return err
	}
	_, _ = runner.Run(ctx, "sudo", "launchctl", "bootout", "system/"+tunnel.Label)
	if _, err := runner.Run(ctx, "sudo", "launchctl", "bootstrap", "system", tunnel.PlistPath); err != nil {
		return err
	}
	p.Line("  %s LaunchDaemon installed and bootstrapped", ui.StyleSuccess.Render("✓"))
	return nil
}

func printAccessGuide(p *Printer, hostname string) {
	p.Section("Cloudflare Access")
	p.Line("  1. Open https://one.dash.cloudflare.com/")
	p.Line("  2. Create a Self-hosted Access app for %s", hostname)
	p.Line("  3. Add an Allow policy for your account or team")
	p.Line("  4. Optional: enable browser-rendered SSH")
	p.Blank()
	p.Line("Client:")
	p.Line("  brew install cloudflared")
	p.Line("  dot tunnel client add %s", hostname)
	p.Line("  ssh <user>@%s", hostname)
	p.Blank()
	p.Line("Optional hardening:")
	p.Line("  echo 'PasswordAuthentication no' | sudo tee /etc/ssh/sshd_config.d/99-dot-tunnel.conf")
}

func tunnelRunner(dryRun bool) *exec.Runner {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return exec.NewRunner(dryRun, logger)
}

func lookupCloudflared() (string, bool) {
	path, err := osexec.LookPath("cloudflared")
	return path, err == nil
}

func lookupBrew() (string, bool) {
	path, err := osexec.LookPath("brew")
	return path, err == nil
}

func cloudflaredVersion(ctx context.Context, runner *exec.Runner, cloudflaredPath string) string {
	result, err := runner.RunQuery(ctx, cloudflaredPath, "--version")
	if err != nil {
		return filepath.Base(cloudflaredPath)
	}
	line := strings.TrimSpace(result.Stdout)
	if line == "" {
		return filepath.Base(cloudflaredPath)
	}
	return line
}

func tunnelStateLabel(state config.UserTunnelState) string {
	if state.TunnelName == "" && state.TunnelID == "" {
		return "(unset)"
	}
	if state.TunnelID == "" {
		return state.TunnelName
	}
	return fmt.Sprintf("%s (%s)", state.TunnelName, state.TunnelID)
}

func filePresence(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return "missing"
}

func firstExistingPath(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func rejectTunnelHomeOverride(cmd *cobra.Command) error {
	if homeOverride, _ := cmd.Flags().GetString("home"); homeOverride != "" {
		return fmt.Errorf("--home is only supported for 'dot tunnel client'; server commands manage this Mac's system daemon")
	}
	return nil
}
