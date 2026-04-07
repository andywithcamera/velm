package db

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// ConnectToDB initializes the connection to the PostgreSQL database
func ConnectToDB() error {
	// Load .env when present (local dev); ignore missing file — Docker/Kubernetes often set env vars only.
	_ = godotenv.Load()

	// Retrieve database URL from the environment variable
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		return fmt.Errorf("DATABASE_URL not set in environment")
	}

	// Establish connection pool
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return fmt.Errorf("unable to parse database config: %v", err)
	}
	config.ConnConfig.Tracer = &requestMetricsTracer{}

	var err2 error
	Pool, err2 = pgxpool.NewWithConfig(context.Background(), config)
	if err2 != nil {
		return fmt.Errorf("unable to connect to database: %v", err2)
	}

	// Ping the database to confirm connection
	err = Pool.Ping(context.Background())
	if err != nil {
		return fmt.Errorf("unable to ping the database: %v", err)
	}

	log.Println("Successfully connected to the database")
	return nil
}

// CloseDB closes the database connection pool
func CloseDB() {
	Pool.Close()
	log.Println("Database connection closed")
}
