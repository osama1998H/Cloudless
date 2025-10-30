package main

import (
	"fmt"
	"time"

	"github.com/osama1998H/Cloudless/pkg/mtls"
	"go.uber.org/zap"
)

func main() {
	logger, _ := zap.NewDevelopment()

	// Use the same secret as in docker-compose.yml
	secret := []byte("dev-secret-fixed-key-for-testing")

	tm, err := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: secret,
		Logger:     logger,
	})
	if err != nil {
		panic(err)
	}

	// Generate tokens for the 3 agents
	agents := []struct {
		NodeID   string
		NodeName string
		Region   string
		Zone     string
	}{
		{"agent-1", "agent-1", "local", "zone-a"},
		{"agent-2", "agent-2", "local", "zone-b"},
		{"agent-3", "agent-3", "local", "zone-a"},
	}

	for _, agent := range agents {
		token, err := tm.GenerateToken(
			agent.NodeID,
			agent.NodeName,
			agent.Region,
			agent.Zone,
			365*24*time.Hour, // 1 year
			999,              // max uses
		)
		if err != nil {
			panic(err)
		}
		fmt.Printf("# %s (%s/%s)\n", agent.NodeName, agent.Region, agent.Zone)
		fmt.Printf("%s\n\n", token.Token)
	}
}
