package checks

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/flanksource/kommons"

	"github.com/docker/docker/api/types"
	"github.com/flanksource/canary-checker/api/external"
	v1 "github.com/flanksource/canary-checker/api/v1"
	"github.com/flanksource/canary-checker/pkg"
)

type DockerPushChecker struct {
	kommons *kommons.Client `yaml:"-" json:"-"`
}

func (c *DockerPushChecker) SetClient(client *kommons.Client) {
	c.kommons = client
}

func (c *DockerPushChecker) Run(canary v1.Canary) []*pkg.CheckResult {
	var results []*pkg.CheckResult
	for _, conf := range canary.Spec.DockerPush {
		results = append(results, c.Check(canary, conf))
	}
	return results
}

// Type: returns checker type
func (c *DockerPushChecker) Type() string {
	return "dockerPush"
}

// Run: Check every entry from config according to Checker interface
// Returns check result and metrics
func (c *DockerPushChecker) Check(canary v1.Canary, extConfig external.Check) *pkg.CheckResult {
	check := extConfig.(v1.DockerPushCheck)
	start := time.Now()
	ctx := context.Background()
	namespace := canary.Namespace
	var err error
	auth, err := GetAuthValues(check.Auth, c.kommons, namespace)
	if err != nil {
		return Failf(check, "failed to fetch auth details: %v", err)
	}
	authConfig := types.AuthConfig{
		Username: auth.Username.Value,
		Password: auth.Password.Value,
	}
	encodedJSON, _ := json.Marshal(authConfig)
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)
	out, err := dockerClient.ImagePush(ctx, check.Image, types.ImagePushOptions{RegistryAuth: authStr})
	if err != nil {
		return Failf(check, "Failed to push image: %s", err)
	}

	buf := new(bytes.Buffer)
	defer out.Close()
	_, _ = buf.ReadFrom(out)
	lines := strings.Split(buf.String(), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		decodedResponse := struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}{}
		err = json.Unmarshal([]byte(line), &decodedResponse)
		if err != nil {
			return Failf(check, "Invalid response: %v: %s", err, line)
		}
		if decodedResponse.Error != "" {
			return Failf(check, "Failed to push %v", decodedResponse.Error)
		}
	}

	return &pkg.CheckResult{
		Check:    check,
		Pass:     true,
		Duration: time.Since(start).Milliseconds(),
		Metrics:  []pkg.Metric{},
	}
}
