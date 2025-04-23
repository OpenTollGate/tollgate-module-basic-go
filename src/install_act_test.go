package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

func TestMain(m *testing.M) {
	// Setup code here
	m.Run()
	// Teardown code here
}

type jsonMessage struct {
	Stream string `json:"stream"`
	Error  string `json:"error"`
}

func setupDocker(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}

	// Check if Docker is installed (implicitly done by creating a client)

	// Pull required openwrt/sdk image
	out, err := cli.ImagePull(context.Background(), "openwrt/sdk:mediatek-filogic-23.05.3", image.PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	// Build the Docker image for act
	buildCtx, err := os.ReadFile("../Dockerfile-act")
	if err != nil {
		t.Fatal(err)
	}
	buildResp, err := cli.ImageBuild(context.Background(), bytes.NewBuffer(buildCtx), types.ImageBuildOptions{Tags: []string{"act-image"}})
	if err != nil {
		t.Fatal(err)
	}

	dec := json.NewDecoder(buildResp.Body)
	for {
		var msg jsonMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if msg.Error != "" {
			t.Fatal(msg.Error)
		}
		if msg.Stream != "" {
			t.Log(msg.Stream)
		}
	}
	}
	dec := json.NewDecoder(buildResp.Body)
	for {
		var msg jsonMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		if msg.Error != "" {
			t.Fatal(msg.Error)
		}
		if msg.Stream != "" {
			t.Log(msg.Stream)
		}
	}
	defer buildResp.Body.Close()

	// Run the act-image container
	config := &container.Config{Image: "act-image"}
	hostConfig := &container.HostConfig{Binds: []string{"/var/run/docker.sock:/var/run/docker.sock"}}
	resp, err := cli.ContainerCreate(context.Background(), config, hostConfig, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	err = cli.ContainerStart(context.Background(), resp.ID, container.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallActFixture(t *testing.T) {
	setupDocker(t)
}