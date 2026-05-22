package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

type FirebaseConfig struct {
	App    *firebase.App
	Auth   *auth.Client
}

func InitFirebase() (*FirebaseConfig, error) {
	serviceAccountJSON := os.Getenv("FIREBASE_SERVICE_ACCOUNT")
	if serviceAccountJSON == "" {
		return nil, fmt.Errorf("FIREBASE_SERVICE_ACCOUNT env var is required")
	}

	var credentials map[string]interface{}
	if err := json.Unmarshal([]byte(serviceAccountJSON), &credentials); err != nil {
		return nil, fmt.Errorf("invalid FIREBASE_SERVICE_ACCOUNT JSON: %w", err)
	}

	opt := option.WithCredentialsJSON([]byte(serviceAccountJSON))
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		return nil, fmt.Errorf("firebase.NewApp: %w", err)
	}

	authClient, err := app.Auth(context.Background())
	if err != nil {
		return nil, fmt.Errorf("app.Auth: %w", err)
	}

	return &FirebaseConfig{App: app, Auth: authClient}, nil
}
