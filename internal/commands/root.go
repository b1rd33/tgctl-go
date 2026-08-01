package commands

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/b1rd33/tgctl-go/internal/accounts"
	"github.com/b1rd33/tgctl-go/internal/output"
)

type RootConfig struct {
	ReadOnly        bool
	LockWaitSeconds float64
	Full            bool
	Account         string
	// ExitCode is set by command runners that emit their own envelope so the
	// process exit matches the dispatch classification rather than cobra's
	// default exit-1-on-RunE-error.
	ExitCode int
}

type rootConfigKey struct{}

func NewRootCommand() *cobra.Command {
	cfg := &RootConfig{}
	cmd := &cobra.Command{
		Use:          "tg",
		Short:        "Telegram agent CLI",
		Long:         "Telegram agent CLI — read/write/listen against your own Telegram account.",
		Version:      semverVersion(),
		SilenceUsage: true,
		Run: func(c *cobra.Command, _ []string) {
			fmt.Fprintln(c.ErrOrStderr(), c.Long)
		},
	}
	cmd.SetVersionTemplate("{{.Version}}\n")
	cmd.PersistentFlags().BoolVar(&cfg.ReadOnly, "read-only", false, "Reject any write to Telegram or local DB. Also via TG_READONLY=1.")
	cmd.PersistentFlags().Float64Var(&cfg.LockWaitSeconds, "lock-wait", 0, "Seconds to wait for the Telegram session lock (default 0 = fail-fast).")
	cmd.PersistentFlags().BoolVar(&cfg.Full, "full", false, "Disable column truncation in human-mode output.")
	cmd.PersistentFlags().StringVar(&cfg.Account, "account", "", "Account name (uses accounts/<NAME>/). Default selected via accounts-use or TG_ACCOUNT env.")
	cmd.SetContext(context.WithValue(context.Background(), rootConfigKey{}, cfg))
	registerVersion(cmd)
	return cmd
}

func RootConfigFrom(cmd *cobra.Command) RootConfig {
	if cfg, ok := cmd.Context().Value(rootConfigKey{}).(*RootConfig); ok && cfg != nil {
		return *cfg
	}
	return RootConfig{}
}

func registerVersion(root *cobra.Command) {
	v := &cobra.Command{
		Use:          "version",
		Short:        "Print build version",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env := output.Success("version", map[string]any{
				"version": semverVersion(),
				"commit":  shortCommit(),
			}, output.NewRequestID(), nil)
			code := output.Emit(env, output.EmitOptions{
				JSON:   jsonMode(cmd),
				Stdout: cmd.OutOrStdout(),
				Stderr: cmd.ErrOrStderr(),
			})
			if code != output.OK {
				return fmt.Errorf("version failed")
			}
			return nil
		},
	}
	AddOutputFlags(v)
	root.AddCommand(v)
}

func semverVersion() string {
	if base, _, ok := parseGitDescribe(Version); ok {
		return base
	}
	return Version
}

// parseGitDescribe recognizes the output shape produced by
// `git describe --tags --dirty --always` when the nearest tag is a
// v-prefixed semantic version. A plain SemVer prerelease is intentionally not
// split merely because it contains a hyphen.
func parseGitDescribe(value string) (base, commit string, ok bool) {
	described := value
	if strings.HasSuffix(described, "-dirty") {
		described = strings.TrimSuffix(described, "-dirty")
	}
	commitSeparator := strings.LastIndex(described, "-g")
	if commitSeparator < 0 {
		return "", "", false
	}
	commit = described[commitSeparator+2:]
	beforeCommit := described[:commitSeparator]
	countSeparator := strings.LastIndexByte(beforeCommit, '-')
	if countSeparator < 0 {
		return "", "", false
	}
	base = beforeCommit[:countSeparator]
	count := beforeCommit[countSeparator+1:]
	if !strings.HasPrefix(base, "v") || !isSemanticVersion(base) || !allDecimal(count) || len(commit) < 4 || !allHex(commit) {
		return "", "", false
	}
	return base, commit, true
}

func isSemanticVersion(value string) bool {
	if strings.HasPrefix(value, "v") {
		value = strings.TrimPrefix(value, "v")
	}
	if value == "" || strings.Count(value, "+") > 1 {
		return false
	}
	if beforeBuild, build, found := strings.Cut(value, "+"); found {
		if !validIdentifiers(build, false) {
			return false
		}
		value = beforeBuild
	}
	if beforePrerelease, prerelease, found := strings.Cut(value, "-"); found {
		if !validIdentifiers(prerelease, true) {
			return false
		}
		value = beforePrerelease
	}
	core := strings.Split(value, ".")
	if len(core) != 3 {
		return false
	}
	for _, part := range core {
		if !validCoreNumber(part) {
			return false
		}
	}
	return true
}

func validCoreNumber(value string) bool {
	return allDecimal(value) && (len(value) == 1 || value[0] != '0')
}

func validIdentifiers(value string, prerelease bool) bool {
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, char := range []byte(identifier) {
			if !isASCIIDigit(char) {
				numeric = false
			}
			if !isASCIIDigit(char) && !isASCIIAlpha(char) && char != '-' {
				return false
			}
		}
		if prerelease && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func allDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range []byte(value) {
		if !isASCIIDigit(char) {
			return false
		}
	}
	return true
}

func allHex(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range []byte(value) {
		if !isASCIIDigit(char) && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

func isASCIIDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func isASCIIAlpha(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func shortCommit() string {
	if rev := vcsRevision(); rev != "" {
		if len(rev) > 12 {
			return rev[:12]
		}
		return rev
	}
	if _, commit, ok := parseGitDescribe(Version); ok {
		return commit
	}
	if !strings.HasPrefix(Version, "v") && Version != "dev" {
		return Version
	}
	return ""
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}

func ExecuteRoot(root *cobra.Command) int {
	err := root.Execute()
	cfg := rootConfigPtr(root)
	if cfg != nil && cfg.ExitCode != 0 {
		return cfg.ExitCode
	}
	if err != nil {
		_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
		return 1
	}
	return 0
}

// rootConfigPtr exposes the live *RootConfig so command runners can write
// their dispatch-issued exit code back into it. Distinct from RootConfigFrom,
// which returns a copy for read-only consumers.
func rootConfigPtr(cmd *cobra.Command) *RootConfig {
	if v, ok := cmd.Context().Value(rootConfigKey{}).(*RootConfig); ok {
		return v
	}
	return nil
}

func Execute() int {
	return ExecuteRoot(NewRootCommand())
}

// RegisterAll wires every command group onto root. The accounts manager is
// the source of truth for per-account paths and also receives accounts-*
// subcommand calls.
func RegisterAll(root *cobra.Command, mgr *accounts.Manager, cfg CommandsConfig) {
	registerAuth(root, mgr)
	registerReadCommands(root, mgr)
	registerWriteCommands(root, cfg)
	registerMediaCommands(root, cfg)
	registerTopicCommands(root, cfg)
	registerFolderCommands(root, cfg)
	registerDestructiveCommands(root, cfg)
	registerAdminCommands(root, cfg)
	registerLocalDBCommands(root, cfg)
	registerLiveCommands(root, cfg)
	registerAccountCommands(root, mgr)
	registerDoctor(root, mgr)
	registerLogin(root, mgr)
	registerImportTelethon(root, mgr)
	registerSendByUsername(root, mgr)
	registerBackfillEntities(root, mgr)
	installLegacyMigrationPreflight(root, mgr)
}
