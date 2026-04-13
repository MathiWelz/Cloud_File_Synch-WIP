package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"cloudsync/config"
	"cloudsync/notify"
	"cloudsync/providers"
	"cloudsync/sync"
)

const version = "1.0.0"

func main() {
	printBanner()
	fmt.Printf("Platform: %s / %s\n\n", runtime.GOOS, runtime.GOARCH)

	// ── Graceful shutdown ───────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println("\n⚠️  Interrupt received — finishing current operation and exiting cleanly...")
		cancel()
	}()

	// ── Config ─────────────────────────────────────────────────────
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("❌ Failed to load config from %q: %v\n\nTip: Copy config.example.yaml to config.yaml and fill in your credentials.", cfgPath, err)
	}
	fmt.Printf("✅ Config loaded from %q\n", cfgPath)
	fmt.Printf("📦 Providers configured: %d\n\n", len(cfg.Providers))

	// ── Providers ──────────────────────────────────────────────────
	var providerList []providers.Provider
	for _, pc := range cfg.Providers {
		if !pc.Enabled {
			fmt.Printf("⏭️  Skipping disabled provider: %s\n", pc.Name)
			continue
		}
		p, err := providers.New(ctx, pc)
		if err != nil {
			log.Printf("⚠️  Could not init provider %q: %v", pc.Name, err)
			continue
		}
		providerList = append(providerList, p)
		fmt.Printf("✅ Provider ready: %s [%s]\n", pc.Name, pc.Type)
	}
	fmt.Println()

	if len(providerList) == 0 {
		log.Fatal("❌ No providers available. Check your config and credentials.")
	}

	// ── Sync ───────────────────────────────────────────────────────
	syncer := sync.New(cfg, providerList)
	report, err := syncer.Run(ctx, promptUser)
	if err != nil {
		log.Printf("⚠️  Sync completed with errors: %v", err)
	}

	fmt.Println(report.Summary())

	// ── Email Notification ─────────────────────────────────────────
	if cfg.Notification.Enabled {
		fmt.Print("📧 Sending email report... ")
		if err := notify.SendReport(cfg.Notification, report); err != nil {
			fmt.Printf("failed: %v\n", err)
		} else {
			fmt.Println("sent!")
		}
	}
}

func promptUser(message string) bool {
	fmt.Printf("\n⚠️  %s\n", message)
	fmt.Print("Continue anyway? [y/N]: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func printBanner() {
	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Printf("║     ☁  CloudSync v%-18s║\n", version+"  ")
	fmt.Println("╚══════════════════════════════════════╝")
}
