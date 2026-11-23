package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/internal/migrations"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/config"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/database"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	db, err := database.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database connection failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("Applying database migrations...")
	if err := database.Migrate(ctx, db, migrations.FS, "."); err != nil {
		fmt.Fprintf(os.Stderr, "Migration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Migrations applied successfully.")
}
