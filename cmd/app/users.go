package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
	appdb "github.com/prasenjit-net/orchestra/internal/database"
	"github.com/prasenjit-net/orchestra/internal/logging"
)

var resetPasswordFile string

var usersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage local users outside the web UI",
}

var resetPasswordCmd = &cobra.Command{
	Use:   "reset-password USERNAME",
	Short: "Reset a local user password and revoke active sessions",
	Args:  cobra.ExactArgs(1),
	RunE:  runResetPassword,
}

func init() {
	resetPasswordCmd.Flags().StringVar(&resetPasswordFile, "password-file", "", "Read the new password from a file, or - for stdin")
	_ = resetPasswordCmd.MarkFlagRequired("password-file")
	usersCmd.AddCommand(resetPasswordCmd)
}

func runResetPassword(cmd *cobra.Command, args []string) error {
	password, err := readRecoveryPassword(cmd, resetPasswordFile)
	if err != nil {
		return err
	}
	cfg, err := config.Load(viper.GetViper())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	db, dialect, err := appdb.Open(cmd.Context(), cfg.Workflow)
	if err != nil {
		return fmt.Errorf("open application database: %w", err)
	}
	defer db.Close()
	service, err := auth.NewService(cmd.Context(), db, dialect, cfg.Auth, logging.New(cfg.Logging))
	if err != nil {
		return fmt.Errorf("open authentication service: %w", err)
	}
	user, err := service.RecoverPassword(cmd.Context(), args[0], password)
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			return fmt.Errorf("user %q not found", args[0])
		}
		return fmt.Errorf("reset password: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Password reset for %s. Active sessions were revoked and a password change is required at next login.\n", user.Username)
	return nil
}

func readRecoveryPassword(cmd *cobra.Command, path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4097))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(data) > 4096 {
		return "", errors.New("password input exceeds 4096 bytes")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if password == "" {
		return "", errors.New("password input is empty")
	}
	return password, nil
}
