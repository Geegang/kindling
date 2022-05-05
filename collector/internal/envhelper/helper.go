package envhelper

import (
	"fmt"
	"os"
)

func GetHostIpFromEnv() (string, error) {
	return GetValueFromEnv("MY_NODE_IP", "unknown")
}

func GetHostNameFromEnv() (string, error) {
	return GetValueFromEnv("MY_NODE_NAME", "unknown")
}

func GetValueFromEnv(env string, defaultValue string) (string, error) {
	value, ok := os.LookupEnv(env)
	if !ok {
		return defaultValue, fmt.Errorf("[%s] is not found in env variable which will be set [%s]", env, defaultValue)
	}
	return value, nil
}
