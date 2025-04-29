package janitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"errors"

	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
)

type packageEvent struct {
	event      *nostr.Event
	packageURL string
}

type JanitorConfig struct {
	Relays             []string `json:"relays"`
	TrustedMaintainers []string `json:"trusted_maintainers"`
	PackageInfo        struct {
		Version   string `json:"version"`
		Timestamp int64  `json:"timestamp"`
	} `json:"package_info"`
}

func LoadJanitorConfig(path string) (*JanitorConfig, error) {
	fmt.Println("Loading configuration from", path)
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("Error reading config file: %v", err)
		return nil, err
	}

	var config JanitorConfig
	err = json.Unmarshal(data, &config)
	if err != nil {
		log.Printf("Error unmarshaling config: %v", err)
		return nil, err
	}

	fmt.Println("Configuration loaded:", config)
	return &config, nil
}

type Janitor struct {
	relays             []string
	trustedMaintainers []string
	currentVersion     *version.Version
	currentTimestamp   int64
	configPath         string
	opkgCmd            string
}

func NewJanitor(relays []string, trustedMaintainers []string, currentVersion string, currentTimestamp int64, configPath string) (*Janitor, error) {
	fmt.Println("Creating new Janitor instance")
	v, err := version.NewVersion(currentVersion)
	if err != nil {
		log.Printf("Invalid current version: %v", err)
		return nil, err
	}
	return &Janitor{
		relays:             relays,
		trustedMaintainers: trustedMaintainers,
		currentVersion:     v,
		currentTimestamp:   currentTimestamp,
		configPath:         configPath,
		opkgCmd:            "opkg",
	}, nil
}

func (j *Janitor) ListenForNIP94Events() {
	log.Println("Starting to listen for NIP-94 events")
	ctx := context.Background()
	relayPool := nostr.NewSimplePool(ctx)
	var wg sync.WaitGroup
	eventChan := make(chan *nostr.Event, 1000) // Buffered channel to handle events from multiple relays

	for _, relayURL := range j.relays {
		wg.Add(1)
		go func(relayURL string) {
			defer wg.Done()
			fmt.Printf("Connecting to relay: %s\n", relayURL)
			relay, err := relayPool.EnsureRelay(relayURL)
			if err != nil {
				log.Printf("Failed to connect to relay %s: %v", relayURL, err)
				log.Printf("EnsureRelay error details: %+v", err)
				return
			}
			fmt.Printf("Connected to relay: %s\n", relayURL)

			sub, err := relay.Subscribe(ctx, []nostr.Filter{
				{
					Kinds: []int{1063}, // NIP-94 event kind
				},
			})
			if err != nil {
				log.Printf("Failed to subscribe to NIP-94 events on relay %s: %v", relayURL, err)
				log.Printf("Subscription error details: %+v", err)
				return
			}
			fmt.Printf("Subscription successful on relay %s\n", relayURL)

			fmt.Printf("Subscribed to NIP-94 events on relay %s\n", relayURL)
			for event := range sub.Events {
				//log.Printf("Received NIP-94 event from relay %s: %s", relayURL, event.ID)
				eventChan <- event
			}
			fmt.Printf("Stopped listening for NIP-94 events on relay %s\n", relayURL)
		}(relayURL)
	}

	go func() {
		wg.Wait()
		close(eventChan) // Close the channel when all relays are done
	}()

	eventMap := make(map[string]*packageEvent)
	totalEvents := 0
	untrustedEventCount := 0
	trustedEventCount := 0
	collisionCount := 0

	timer := time.NewTimer(10 * time.Second)
	timer.Stop()
	isTimerActive := false
	fmt.Println("Starting event processing loop")
	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				log.Println("eventChan closed, stopping event processing")
				return
			}
			totalEvents++
			if !contains(j.trustedMaintainers, event.PubKey) {
				untrustedEventCount++
				continue
			}

			trustedEventCount++
			ok, err := event.CheckSignature()
			if err != nil || !ok {
				//log.Printf("Invalid signature for NIP-94 event %s: %v", event.ID, err)
				continue
			}

			packageURL, versionStr, filename, timestamp, err := parseNIP94Event(*event)
			if err != nil {
				continue
			}

			//fmt.Printf("Received event from channel: ID=%s, URL=%s, Version=%s, Filename=%s, Timestamp=%d",
			//	event.ID, packageURL, versionStr, filename, timestamp)
			key := fmt.Sprintf("%s-%s", filename, versionStr)
			existingPackageEvent, ok := eventMap[key]
			if ok {
				collisionCount++
				if timestamp > int64(existingPackageEvent.event.CreatedAt) {
					eventMap[key] = &packageEvent{
						event:      event,
						packageURL: packageURL,
					}
					fmt.Printf("Newer version with timestamp %d detected for file %s, version %s\n", timestamp, filename, versionStr)
					if !isTimerActive {
						timer.Reset(10 * time.Second)
						isTimerActive = true
					}
				}
			} else {
				fmt.Printf("Found occurrence of package %s, version %s, timestamp %d\n", filename, versionStr, timestamp)
				eventMap[key] = &packageEvent{
					event:      event,
					packageURL: packageURL,
				}
				if timestamp > j.currentTimestamp {
					if !isTimerActive {
						timer.Reset(10 * time.Second)
						isTimerActive = true
					}
				}
			}
		case <-timer.C:
			log.Println("Timeout reached, checking for new versions")
			for _, packageEvent := range eventMap {
				event := packageEvent.event
				_, versionStr, _, timestamp, err := parseNIP94Event(*event)
				if err != nil {
					log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
					continue
				}
				if isNewerVersion(versionStr, timestamp, j.currentVersion, j.currentTimestamp) {
					fmt.Printf("Newer package version available: %s\n", versionStr)
					pkg, err := j.DownloadPackage(packageEvent.packageURL)
					if err != nil {
						log.Printf("Error downloading package: %v", err)
						continue
					}
					err = j.verifyPackageChecksum(pkg, *event)
					if err != nil {
						log.Printf("Error verifying package checksum: %v", err)
						continue
					}
					err = j.InstallPackage(pkg)
					if err != nil {
						log.Printf("Error installing package: %v", err)
						continue
					}
					fmt.Printf("Successfully installed new package version: %s\n", versionStr)
					RunPostInstallScript(j.configPath, versionStr)
				}
			}
			timer.Stop()
			isTimerActive = false
		}
	}
}

func (j *Janitor) DownloadPackage(url string) ([]byte, error) {
	fmt.Printf("Downloading package from %s to /tmp/\n", url)

	tmpFile, err := os.CreateTemp("/tmp/", "package-*.ipk")
	if err != nil {
		log.Printf("Error creating temp file: %v", err)
		return nil, err
	}
	defer os.Remove(tmpFile.Name())

	cmd := exec.Command("wget", "-O", tmpFile.Name(), url)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error downloading package: %v, output: %s", err, output)
		return nil, err
	}

	pkg, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		log.Printf("Error reading downloaded package: %v", err)
		return nil, err
	}

	fmt.Println("Package downloaded successfully to /tmp/")
	return pkg, nil
}

type progressLogger struct {
	total      int64
	downloaded *int64
	lastLog    time.Time
}

func (p *progressLogger) Write(b []byte) (int, error) {
	n := len(b)
	*p.downloaded += int64(n)
	now := time.Now()
	if now.Sub(p.lastLog) > time.Second {
		if p.total == -1 {
			log.Printf("Download progress: %d bytes (total size unknown)", *p.downloaded)
		} else {
			log.Printf("Download progress: %d/%d bytes (%.2f%%)", *p.downloaded, p.total, float64(*p.downloaded)/float64(p.total)*100)
		}
		p.lastLog = now
	}
	return n, nil
}

func (j *Janitor) verifyPackageChecksum(pkg []byte, event nostr.Event) error {
	log.Println("Verifying package checksum")
	for _, tag := range event.Tags {
		if len(tag) > 0 && tag[0] == "x" && len(tag) > 1 {
			expectedHash := tag[1]
			actualHash := sha256.Sum256(pkg)
			if expectedHash != hex.EncodeToString(actualHash[:]) {
				return fmt.Errorf("package checksum verification failed")
			}
			log.Println("Package checksum verified successfully")
		}
	}
	return nil
}

func (j *Janitor) InstallPackage(pkg []byte) error {
	fmt.Printf("Installing package")
	tmpFile, err := os.CreateTemp("", "package.ipk")
	if err != nil {
		log.Printf("Error creating temp file: %v", err)
		return err
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(pkg); err != nil {
		log.Printf("Error writing package to temp file: %v", err)
		return err
	}
	tmpFile.Close()

	cmd := exec.Command(j.opkgCmd, "install", tmpFile.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error installing package: %v, output: %s", err, output)
		return err
	}

	fmt.Printf("Package installed successfully")
	return nil
}

func isNetworkUnreachable(err error) bool {
	if err == nil {
		return false
	}
	// Check if the error is related to network unreachability
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial" && opErr.Net == "tcp" && opErr.Err.Error() == "connect: network is unreachable"
	}
	return false
}

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

// parseNIP94Event extracts package information from a NIP-94 event
func parseNIP94Event(event nostr.Event) (string, string, string, int64, error) {
	requiredTags := []string{"url", "version", "arch", "branch", "filename"}
	tagMap := make(map[string]string)

	for _, tag := range event.Tags {
		if len(tag) > 0 && len(tag) > 1 {
			tagMap[tag[0]] = tag[1]
		}
	}

	// Check if all required tags are present
	for _, tag := range requiredTags {
		if _, ok := tagMap[tag]; !ok {
			return "", "", "", 0, fmt.Errorf("invalid NIP-94 event: missing required tag '%s'", tag)
		}
	}

	url := tagMap["url"]
	version := tagMap["version"]
	// arch := tagMap["arch"]
	// branch := tagMap["branch"]
	filename := tagMap["filename"]
	timestamp := int64(event.CreatedAt)

	// log.Printf("Parsed NIP-94 event: url=%s, version=%s, arch=%s, branch=%s, filename=%s, timestamp=%d",
	//	url, version, arch, branch, filename, timestamp)

	if url == "" || version == "" || timestamp == 0 {
		return "", "", "", 0, fmt.Errorf("invalid NIP-94 event: missing required tags")
	}

	return url, version, filename, timestamp, nil
}

func isNewerVersion(newVersion string, newTimestamp int64, currentVersion *version.Version, currentTimestamp int64) bool {
    log.Printf("Comparing versions: newVersion=%s, newTimestamp=%d, currentVersion=%s, currentTimestamp=%d",
        newVersion, newTimestamp, currentVersion, currentTimestamp)
	newVersionObj, err := version.NewVersion(newVersion)
	if err != nil {
		//log.Printf("Invalid new version: %v", err)
		return false
	}
	return newVersionObj.GreaterThan(currentVersion) && newTimestamp > currentTimestamp
}

func RunPostInstallScript(configPath, newVersion string) {
	fmt.Printf("Running post-install script")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("Error reading config file: %v", err)
		return
	}

	var configMap map[string]interface{}
	err = json.Unmarshal(configData, &configMap)
	if err != nil {
		log.Printf("Error unmarshaling config: %v", err)
		return
	}

	newTimestamp := time.Now().Unix()
	configMap["package_info"] = map[string]interface{}{
		"version":   newVersion,
		"timestamp": newTimestamp,
	}

	data, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		log.Printf("Error marshaling config: %v", err)
		return
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		log.Printf("Error writing config file: %v", err)
		return
	}

	fmt.Printf("Post-install script completed successfully")
}
