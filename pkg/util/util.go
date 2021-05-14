package util

import (
	"fmt"
	"os"
	"strconv"

	"knative.dev/pkg/network"
)

const (
	// DefaultNatssURLKey is the environment variable that can be set to specify the natss url
	defaultNatssURLVar  = "DEFAULT_NATSS_URL"
	defaultClusterIDVar = "DEFAULT_CLUSTER_ID"
	ackWaitMinutesVar   = "ACK_WAIT_MINUTES"
	maxInflightVar      = "MAX_INFLIGHT"

	fallbackDefaultNatssURLTmpl = "nats://nats-streaming.natss.svc.%s:4222"
	fallbackDefaultClusterID    = "knative-nats-streaming"
	fallbackAckWaitMinutes      = 1
	fallbackMaxInflight         = 1024

	defaultMaxIdleConnections        = 1000
	defaultMaxIdleConnectionsPerHost = 100

	clientID = "natss-ch-dispatcher"
)

type NatssConfig struct {
	ClientID            string
	MaxIdleConns        int
	MaxIdleConnsPerHost int
}

func GetNatssConfig() NatssConfig {
	return NatssConfig{
		ClientID:            clientID,
		MaxIdleConns:        getMaxIdleConnections(),
		MaxIdleConnsPerHost: getMaxIdleConnectionsPerHost(),
	}
}

// GetDefaultNatssURL returns the default natss url to connect to
func GetDefaultNatssURL() string {
	return getEnv(defaultNatssURLVar, fmt.Sprintf(fallbackDefaultNatssURLTmpl, network.GetClusterDomainName()))
}

// GetDefaultClusterID returns the default cluster id to connect with
func GetDefaultClusterID() string {
	return getEnv(defaultClusterIDVar, fallbackDefaultClusterID)
}

func GetAckWaitMinutes() int {
	return getEnvAsInt(ackWaitMinutesVar, fallbackAckWaitMinutes)
}

func GetMaxInflight() int {
	return getEnvAsInt(maxInflightVar, fallbackMaxInflight)
}

// getMaxIdleConnections returns the max number of idle connections
func getMaxIdleConnections() int {
	return defaultMaxIdleConnections
}

// getMaxIdleConnections returns the max number of idle connections per host
func getMaxIdleConnectionsPerHost() int {
	return defaultMaxIdleConnectionsPerHost
}

func getEnv(envKey string, fallback string) string {
	val, ok := os.LookupEnv(envKey)
	if !ok {
		return fallback
	}
	return val
}

func getEnvAsInt(envKey string, fallback int) int {
	valueStr := getEnv(envKey, "")
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return fallback
}
