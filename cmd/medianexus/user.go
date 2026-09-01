package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"
)

// runUser handles `medianexus user ...`. It exists because a fresh install has
// no accounts and therefore no way to log in and make one -- and because
// section 20.1 wants a CLI for recovery when the interface is unreachable.
func runUser(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("user: expected a subcommand; known subcommands: add, list")
	}

	flags := flag.NewFlagSet("user "+args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", defaultConfigPath, "path to config.yml")
	username := flags.String("username", "", "account name")
	role := flags.String("role", string(settings.RoleAdmin), "admin | operator | readonly")
	passwordStdin := flags.Bool("password-stdin", false, "read the password from standard input")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	switch args[0] {
	case "add":
		return addUser(*configPath, strings.TrimSpace(*username), *role, *passwordStdin, out)
	case "list":
		return listUsers(*configPath, out)
	default:
		return fmt.Errorf("user: unknown subcommand %q; known subcommands: add, list", args[0])
	}
}

// openSettings opens settings.db the way serve does, so a CLI command sees
// exactly the same database and migrations.
func openSettings(configPath string) (*settings.Cache, func(), error) {
	fsys := fsx.NewOS()
	loaded, err := config.Load(fsys, configPath)
	if err != nil {
		return nil, nil, err
	}
	if err := config.CheckDirectories(fsys, loaded).OrNil(); err != nil {
		return nil, nil, err
	}

	ctx := context.Background()
	quiet := slog.New(slog.DiscardHandler)
	opened, err := store.Open(ctx, clock.NewSystem(), store.Config{
		Name:        "settings",
		Path:        fmt.Sprintf("%s/settings.db", loaded.Paths.DBDir),
		BusyTimeout: loaded.Database.BusyTimeout.AsDuration(),
		PageSize:    loaded.Database.PageSize,
		CacheSizeKB: loaded.Database.CacheSizeKB,
		Logger:      quiet,
	})
	if err != nil {
		return nil, nil, err
	}

	owner, err := migrations.Settings()
	if err != nil {
		_ = opened.Close()
		return nil, nil, err
	}
	if err := store.Migrate(ctx, opened, loaded.Paths.BackupDir, owner); err != nil {
		_ = opened.Close()
		return nil, nil, err
	}

	cache, err := settings.NewCache(ctx, opened)
	if err != nil {
		_ = opened.Close()
		return nil, nil, err
	}
	return cache, func() { _ = opened.Close() }, nil
}

func addUser(configPath, username, role string, passwordStdin bool, out io.Writer) error {
	if username == "" {
		return errors.New("user add: --username is required")
	}
	parsedRole := settings.Role(role)
	switch parsedRole {
	case settings.RoleAdmin, settings.RoleOperator, settings.RoleReadonly:
	default:
		return fmt.Errorf("user add: unknown role %q; expected admin, operator or readonly", role)
	}

	cache, closeStore, err := openSettings(configPath)
	if err != nil {
		return err
	}
	defer closeStore()

	password, generated, err := resolvePassword(passwordStdin)
	if err != nil {
		return err
	}
	hashed, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	created, err := cache.CreateUser(context.Background(), clock.NewSystem(), username, hashed, parsedRole)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "created %s (id %d, role %s)\n",
		created.Username, created.ID, created.Role); err != nil {
		return err
	}
	if generated {
		// Shown once. Nothing stores the plaintext, so it cannot be recovered.
		_, err = fmt.Fprintf(out, "password: %s\n\nWrite this down; it is not stored anywhere.\n", password)
	}
	return err
}

// resolvePassword either reads one or makes one. A generated password beats a
// typed one here: reading a typed password without echo needs terminal
// handling, and a password typed to make that easy is usually a weak one.
func resolvePassword(fromStdin bool) (string, bool, error) {
	if !fromStdin {
		password, err := generatePassword()
		return password, true, err
	}

	read, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
	if err != nil {
		return "", false, fmt.Errorf("user add: read password: %w", err)
	}
	password := strings.TrimRight(string(read), "\r\n")
	if password == "" {
		return "", false, errors.New("user add: the password on standard input was empty")
	}
	return password, false, nil
}

func generatePassword() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("user add: generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func listUsers(configPath string, out io.Writer) error {
	cache, closeStore, err := openSettings(configPath)
	if err != nil {
		return err
	}
	defer closeStore()

	users := cache.Snapshot().Users()
	if len(users) == 0 {
		_, err := fmt.Fprintln(out, "no accounts; create one with 'medianexus user add --username <name>'")
		return err
	}
	for _, user := range users {
		if _, err := fmt.Fprintf(out, "%-4d %-20s %-10s password=%v\n",
			user.ID, user.Username, user.Role, user.HasPassword); err != nil {
			return err
		}
	}
	return nil
}
