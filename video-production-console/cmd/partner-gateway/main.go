package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"video-production-console/internal/partnergateway"
)

const (
	minPartnerVersion = "0.1.0"
	sessionTTL        = 12 * time.Hour
	shutdownTimeout   = 15 * time.Second
	maxSecretFileSize = 8 * 1024
)

type serveConfig struct {
	listen        string
	databasePath  string
	tlsCertPath   string
	tlsKeyPath    string
	upstreamURL   *url.URL
	upstreamKey   string
	adminPassword string
	capabilities  partnergateway.Capabilities
	aura          partnergateway.AuraRuntime
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: partner-gateway <serve|partner>")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	switch args[0] {
	case "serve":
		set := flag.NewFlagSet("serve", flag.ContinueOnError)
		set.SetOutput(stderr)
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if set.NArg() != 0 {
			return fmt.Errorf("serve does not accept arguments")
		}
		config, err := loadServeConfig()
		if err != nil {
			return err
		}
		return serve(ctx, config, stderr)
	case "partner":
		return runPartner(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func loadServeConfig() (serveConfig, error) {
	var config serveConfig
	var err error

	if config.listen, err = requiredEnv("PARTNER_GATEWAY_LISTEN"); err != nil {
		return serveConfig{}, err
	}
	if config.databasePath, err = requiredEnv("PARTNER_GATEWAY_DB"); err != nil {
		return serveConfig{}, err
	}
	upstream, err := requiredEnv("PARTNER_GATEWAY_UPSTREAM")
	if err != nil {
		return serveConfig{}, err
	}
	config.upstreamURL, err = parseHTTPURL("PARTNER_GATEWAY_UPSTREAM", upstream)
	if err != nil {
		return serveConfig{}, err
	}

	upstreamKeyPath, err := requiredEnv("PARTNER_GATEWAY_UPSTREAM_KEY_FILE")
	if err != nil {
		return serveConfig{}, err
	}
	config.upstreamKey, err = readSecretFile("PARTNER_GATEWAY_UPSTREAM_KEY_FILE", upstreamKeyPath)
	if err != nil {
		return serveConfig{}, err
	}
	auraKeyPath, err := requiredEnv("PARTNER_GATEWAY_AURA_KEY_FILE")
	if err != nil {
		return serveConfig{}, err
	}
	auraKey, err := readSecretFile("PARTNER_GATEWAY_AURA_KEY_FILE", auraKeyPath)
	if err != nil {
		return serveConfig{}, err
	}

	if config.tlsCertPath, err = requiredEnv("PARTNER_GATEWAY_TLS_CERT"); err != nil {
		return serveConfig{}, err
	}
	if config.tlsKeyPath, err = requiredEnv("PARTNER_GATEWAY_TLS_KEY"); err != nil {
		return serveConfig{}, err
	}
	auraBaseURL, err := requiredEnv("PARTNER_GATEWAY_AURA_BASE_URL")
	if err != nil {
		return serveConfig{}, err
	}
	if _, err := parseHTTPURL("PARTNER_GATEWAY_AURA_BASE_URL", auraBaseURL); err != nil {
		return serveConfig{}, err
	}
	auraModel, err := requiredEnv("PARTNER_GATEWAY_AURA_MODEL")
	if err != nil {
		return serveConfig{}, err
	}
	auraVoiceID, err := requiredEnv("PARTNER_GATEWAY_AURA_VOICE_ID")
	if err != nil {
		return serveConfig{}, err
	}
	if adminKeyPath := strings.TrimSpace(os.Getenv("PARTNER_GATEWAY_ADMIN_KEY_FILE")); adminKeyPath != "" {
		config.adminPassword, err = readSecretFile("PARTNER_GATEWAY_ADMIN_KEY_FILE", adminKeyPath)
		if err != nil {
			return serveConfig{}, err
		}
	}

	config.capabilities = defaultCapabilities()
	config.aura = partnergateway.AuraRuntime{
		BaseURL: auraBaseURL,
		APIKey:  auraKey,
		Model:   auraModel,
		VoiceID: auraVoiceID,
		Speed:   1,
		Volume:  1,
	}
	return config, nil
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

func parseHTTPURL(name, value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%s must be a valid HTTP or HTTPS URL", name)
	}
	return parsed, nil
}

func readSecretFile(name, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s secret file must be a regular file", name)
	}
	if runtime.GOOS == "linux" && info.Mode().Perm()&0o004 != 0 {
		return "", fmt.Errorf("%s must not be world-readable", name)
	}
	body, err := io.ReadAll(io.LimitReader(file, maxSecretFileSize+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	if len(body) > maxSecretFileSize {
		return "", fmt.Errorf("%s secret file is too large", name)
	}
	secret := string(body)
	switch {
	case strings.HasSuffix(secret, "\r\n"):
		secret = strings.TrimSuffix(secret, "\r\n")
	case strings.HasSuffix(secret, "\n"):
		secret = strings.TrimSuffix(secret, "\n")
	}
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("%s secret file is empty", name)
	}
	return secret, nil
}

func defaultCapabilities() partnergateway.Capabilities {
	return partnergateway.Capabilities{
		Features:         []string{"text", "image"},
		TextModels:       []string{"gpt-5.6-sol", "grok-4.6"},
		ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
		ImageModel:       "gpt-image-2",
	}
}

func serve(ctx context.Context, config serveConfig, stderr io.Writer) error {
	store, err := partnergateway.OpenStore(config.databasePath)
	if err != nil {
		return err
	}
	defer store.Close()

	auth := partnergateway.NewAuthService(
		store,
		nil,
		nil,
		sessionTTL,
		minPartnerVersion,
		config.capabilities,
		config.aura,
	)
	handler, err := partnergateway.NewServer(partnergateway.ServerOptions{
		Auth:            auth,
		Store:           store,
		Policy:          partnergateway.DefaultPolicy(),
		Limiter:         partnergateway.NewLimiter(),
		UpstreamBaseURL: config.upstreamURL,
		UpstreamAPIKey:  config.upstreamKey,
		AdminPassword:   config.adminPassword,
		Logger:          slog.New(slog.NewTextHandler(stderr, nil)),
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              config.listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.ListenAndServeTLS(config.tlsCertPath, config.tlsKeyPath)
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve partner gateway: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down partner gateway: %w", err)
		}
		if err := <-serveErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve partner gateway: %w", err)
		}
		return nil
	}
}

func runPartner(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: partner-gateway partner <create|list|enable|disable|rotate-key|unbind>")
	}

	action := args[0]
	set := flag.NewFlagSet("partner "+action, flag.ContinueOnError)
	set.SetOutput(stderr)
	databasePath := set.String("db", "", "partner gateway database path")
	var displayName, partnerID *string
	switch action {
	case "create":
		displayName = set.String("name", "", "partner display name")
	case "list":
	case "enable", "disable", "rotate-key", "unbind":
		partnerID = set.String("id", "", "partner ID")
	default:
		return fmt.Errorf("unknown partner command %q", action)
	}
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return fmt.Errorf("partner %s does not accept positional arguments", action)
	}
	if strings.TrimSpace(*databasePath) == "" {
		return fmt.Errorf("partner %s requires --db", action)
	}
	if displayName != nil && strings.TrimSpace(*displayName) == "" {
		return fmt.Errorf("partner create requires --name")
	}
	if partnerID != nil && strings.TrimSpace(*partnerID) == "" {
		return fmt.Errorf("partner %s requires --id", action)
	}

	store, err := partnergateway.OpenStore(*databasePath)
	if err != nil {
		return err
	}
	defer store.Close()
	auth := partnergateway.NewAuthService(
		store,
		nil,
		nil,
		sessionTTL,
		minPartnerVersion,
		defaultCapabilities(),
		partnergateway.AuraRuntime{},
	)

	switch action {
	case "create":
		created, err := auth.CreatePartner(ctx, *displayName)
		if err != nil {
			return err
		}
		fmt.Fprintf(
			stdout,
			"partner_id=%s\ndisplay_name=%s\nactivation_key=%s\n",
			created.PartnerID,
			created.DisplayName,
			created.ActivationKey,
		)
	case "list":
		partners, err := auth.ListPartners(ctx)
		if err != nil {
			return err
		}
		for _, partner := range partners {
			fmt.Fprintf(
				stdout,
				"%s\t%s\t%s\tdevice_bound=%t\ttext_calls=%d\timage_calls=%d\n",
				partner.ID,
				partner.DisplayName,
				partner.Status,
				partner.DeviceHash != "",
				partner.TextCalls,
				partner.ImageCalls,
			)
		}
	case "enable":
		if err := auth.SetPartnerStatus(ctx, *partnerID, partnergateway.PartnerActive); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "partner %s enabled\n", *partnerID)
	case "disable":
		if err := auth.SetPartnerStatus(ctx, *partnerID, partnergateway.PartnerDisabled); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "partner %s disabled\n", *partnerID)
	case "rotate-key":
		key, err := auth.RotateKey(ctx, *partnerID)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "partner_id=%s\nactivation_key=%s\n", *partnerID, key)
	case "unbind":
		if err := auth.UnbindDevice(ctx, *partnerID); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "partner %s unbound\n", *partnerID)
	}
	return nil
}
