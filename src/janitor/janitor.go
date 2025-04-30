package janitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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
		Branch    string `json:"branch"`
		Arch      string `json:"arch"`
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
	ConfigBranch       string
	ConfigArch         string
	configPath         string
	opkgCmd            string
}

func NewJanitor(relays []string, trustedMaintainers []string, currentVersion string, currentTimestamp int64, configBranch string, configArch string, configPath string) (*Janitor, error) {
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
		ConfigBranch:       configBranch,
		ConfigArch:         configArch,
		configPath:         configPath,
		opkgCmd:            "opkg",
	}, nil
}

func (j *Janitor) ListenForNIP94Events() {
	log.Println("Starting to listen for NIP-94 events")
	ctx := context.Background()
	relayPool := nostr.NewSimplePool(ctx)
	eventChan := make(chan *nostr.Event, 1000)

	for {
		var wg sync.WaitGroup
		for _, relayURL := range j.relays {
			wg.Add(1)
			go func(relayURL string) {
				defer wg.Done()
				retryDelay := 5 * time.Second
				for {
					fmt.Printf("Connecting to relay: %s\n", relayURL)
					relay, err := relayPool.EnsureRelay(relayURL)
					if err != nil {
						log.Printf("Failed to connect to relay %s: %v. Retrying in %v...", relayURL, err, retryDelay)
						time.Sleep(retryDelay)
						retryDelay *= 2
						if retryDelay > 1*time.Minute {
							retryDelay = 1 * time.Minute
						}
						continue
					}
					fmt.Printf("Connected to relay: %s\n", relayURL)

					sub, err := relay.Subscribe(ctx, []nostr.Filter{
						{
							Kinds: []int{1063},
						},
					})
					if err != nil {
						log.Printf("Failed to subscribe to NIP-94 events on relay %s: %v", relayURL, err)
						continue
					}
					fmt.Printf("Subscription successful on relay %s\n", relayURL)
					fmt.Printf("Subscribed to NIP-94 events on relay %s\n", relayURL)
					for event := range sub.Events {
						eventChan <- event
					}
					log.Printf("Relay %s disconnected, attempting to reconnect", relayURL)
				}
			}(relayURL)
		}

		go func() {
			wg.Wait()
			close(eventChan)
		}()

		eventMap := make(map[string]*packageEvent)
		totalEvents := 0
		untrustedEventCount := 0
		trustedEventCount := 0
		collisionCount := 0
		rightTimeKeys := make([]string, 0)
		var already_printed bool = false
		rightBranchKeys := make([]string, 0)
		rightArchKeys := make([]string, 0)
		rightVersionKeys := make([]string, 0)

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

				packageURL, versionStr, arch, branch, filename, timestamp, err := parseNIP94Event(*event)
				if err != nil {
					continue
				}

				key := fmt.Sprintf("%s-%s", filename, versionStr)
				ok = eventMap[key] != nil
				if ok {
					// We already recorded an event with this filename and version string
					collisionCount++
					//log.Println("Collision! Already encountered this filename and version in the past...")
				} else {
					// Its the first time we see this filename & version string
					eventMap[key] = &packageEvent{
						event:      event,
						packageURL: packageURL,
					}
				}

				if timestamp > j.currentTimestamp {
					// fmt.Printf("Received event from channel: ID=%s, URL=%s, Version=%s, Filename=%s, Timestamp=%d",
					// 	event.ID, packageURL, versionStr, filename, timestamp)
					rightTimeKeys = append(rightTimeKeys, key)
				}

				if isNewerVersion(versionStr, timestamp, j.currentVersion, j.currentTimestamp) {
					rightVersionKeys = append(rightVersionKeys, key)
				}

				if branch == j.ConfigBranch {
					rightBranchKeys = append(rightBranchKeys, key)
				}

				if arch == j.ConfigArch {
					rightArchKeys = append(rightArchKeys, key)
				}

				intersection := intersect(rightTimeKeys, rightBranchKeys, rightArchKeys, rightVersionKeys)
				if len(intersection) > 0 && !isTimerActive {
					fmt.Printf("Started the timer\n")
					timer.Reset(10 * time.Second)
					isTimerActive = true
					fmt.Printf("Started the timer, NIP-94 timestamp: %d, config timestamp: %d\n", timestamp, j.currentTimestamp)
					fmt.Printf("Current timestamp %d, current version %s\n", j.currentTimestamp, j.currentVersion.String())
				}

				if len(intersection) > 0 && !already_printed {
					printList := func(name string, list []string) {
						if len(list) <= 3 {
							fmt.Printf("%s: %v\n", name, list)
						} else {
							fmt.Printf("%s count: %d\n", name, len(list))
						}
					}
					fmt.Printf("Intersection: %v\n", intersection)
					printList("Right Time Keys", rightTimeKeys)
					printList("Right Branch Keys", rightBranchKeys)
					printList("Right Arch Keys", rightArchKeys)
					printList("Right Version Keys", rightVersionKeys)
					already_printed = true
				}

			case <-timer.C:
				log.Println("Timeout reached, checking for new versions")

				// Compute the intersection of rightTimeKeys, rightBranchKeys, and rightArchKeys.
				intersection := intersect(rightTimeKeys, rightBranchKeys, rightArchKeys, rightVersionKeys)
				qualifyingEventsMap := make(map[string]*packageEvent)

				for _, key := range intersection {
					qualifyingEventsMap[key] = eventMap[key]
				}

				sortedKeys := sortQualifyingEventsByVersion(qualifyingEventsMap)
				fmt.Println("Sorted Qualifying Events Keys:", sortedKeys)

				latestKey := sortedKeys[0]
				latestPackageEvent := qualifyingEventsMap[latestKey]
				if latestPackageEvent == nil {
					log.Println("Latest package event is nil")
					timer.Stop()
					isTimerActive = false
					return
				}

				event := latestPackageEvent.event
				_, versionStr, _, _, _, _, err := parseNIP94Event(*event)
				if err != nil {
					log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
					timer.Stop()
					isTimerActive = false
					return
				}

				fmt.Printf("Newer package version available: %s\n", versionStr)
				checksum := getChecksumFromEvent(*latestPackageEvent.event)
				pkgPath, pkg, err := j.DownloadPackage(latestPackageEvent.packageURL, checksum)
				if err != nil {
					log.Printf("Error downloading package: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}
				err = j.verifyPackageChecksum(pkg, *event)
				if err != nil {
					log.Printf("Error verifying package checksum: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}
				err = j.updateConfigWithPackagePath(pkgPath)
				if err != nil {
					log.Printf("Error updating config with package path: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}
				fmt.Printf("New package version %s is ready to be installed by cronjob\n", versionStr)

				timer.Stop()
				isTimerActive = false
			}
		}
	}
}

func (j *Janitor) DownloadPackage(url string, checksum string) (string, []byte, error) {
	filename := checksum + ".ipk"
	tmpFilePath := filepath.Join("/tmp/", filename)
	fmt.Printf("Downloading package from %s to %s\n", url, tmpFilePath)

	tmpFile, err := os.Create(tmpFilePath)
	if err != nil {
		log.Printf("Error creating file: %v", err)
		return "", nil, err
	}

	cmd := exec.Command("wget", "-O", tmpFile.Name(), url)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Error downloading package: %v, output: %s", err, output)
		return "", nil, err
	}
	var downloaded int64
	progress := &progressLogger{
		total:      getContentLength(url),
		downloaded: &downloaded,
		lastLog:    time.Now(),
	}
	progress.Write(output)

	pkg, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		log.Printf("Error reading downloaded package: %v", err)
		return "", nil, err
	}

	fmt.Println("Package downloaded successfully to /tmp/")
	return tmpFile.Name(), pkg, nil
}

type progressLogger struct {
	total      int64
	downloaded *int64
	lastLog    time.Time
}

func getContentLength(url string) int64 {
	resp, err := http.Head(url)
	if err != nil {
		log.Printf("Error getting content length: %v", err)
		return -1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("Error getting content length: %s", resp.Status)
		return -1
	}
	return resp.ContentLength
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

func (j *Janitor) updateConfigWithPackagePath(pkgPath string) error {
	configPath := j.configPath
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("Error reading config file: %v", err)
		return err
	}

	var config map[string]interface{}
	err = json.Unmarshal(data, &config)
	if err != nil {
		log.Printf("Error unmarshaling config: %v", err)
		return err
	}

	config["update_path"] = pkgPath

	updatedData, err := json.Marshal(config)
	if err != nil {
		log.Printf("Error marshaling updated config: %v", err)
		return err
	}

	err = os.WriteFile(configPath, updatedData, 0644)
	if err != nil {
		log.Printf("Error writing updated config file: %v", err)
		return err
	}
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
func parseNIP94Event(event nostr.Event) (string, string, string, string, string, int64, error) {
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
			return "", "", "", "", "", 0, fmt.Errorf("invalid NIP-94 event: missing required tag '%s'", tag)
		}
	}

	url := tagMap["url"]
	version := tagMap["version"]
	arch := tagMap["arch"]
	branch := tagMap["branch"]
	filename := tagMap["filename"]
	timestamp := int64(event.CreatedAt)

	if url == "" || version == "" || timestamp == 0 {
		return "", "", "", "", "", 0, fmt.Errorf("invalid NIP-94 event: missing required tags")
	}

	return url, version, arch, branch, filename, timestamp, nil
}

func isNewerVersion(newVersion string, newTimestamp int64, currentVersion *version.Version, currentTimestamp int64) bool {
	cleanedNewVersion := strings.Split(newVersion, "+")[0]
	newVersionObj, err := version.NewVersion(cleanedNewVersion)
	if err != nil {
		//log.Printf("Invalid new version: %v", err)
		return false
	}
	cleanedCurrentVersion := strings.Split(currentVersion.String(), "+")[0]
	cleanedCurrentVersionObj, err := version.NewVersion(cleanedCurrentVersion)
	if err != nil {
		//log.Printf("Invalid current version: %v", err)
		return false
	}
	return newVersionObj.GreaterThan(cleanedCurrentVersionObj) && newTimestamp > currentTimestamp
}
func intersect(slices ...[]string) []string {
	if len(slices) == 0 {
		return []string{}
	}
	if len(slices) == 1 {
		return slices[0]
	}
	result := make(map[string]bool)
	for _, key := range slices[0] {
		result[key] = true
	}
	for _, slice := range slices[1:] {
		tempResult := make(map[string]bool)
		for _, key := range slice {
			if result[key] {
				tempResult[key] = true
			}
		}
		result = tempResult
	}
	var intersection []string
	for key := range result {
		intersection = append(intersection, key)
	}
	return intersection
}

// sortQualifyingEventsByVersion sorts the keys of qualifyingEventsMap by version number in descending order
func sortQualifyingEventsByVersion(qualifyingEventsMap map[string]*packageEvent) []string {
	keys := make([]string, 0, len(qualifyingEventsMap))
	for key := range qualifyingEventsMap {
		keys = append(keys, key)
	}

	sort.Slice(keys, func(i, j int) bool {
		versionI := extractVersion(keys[i])
		versionJ := extractVersion(keys[j])
		versionIObj, errI := version.NewVersion(versionI)
		versionJObj, errJ := version.NewVersion(versionJ)
		if errI != nil || errJ != nil {
			// If there's an error parsing versions, fall back to string comparison
			return keys[i] > keys[j]
		}
		return versionIObj.GreaterThan(versionJObj)
	})

	return keys
}

// extractVersion extracts the version string from a key
func extractVersion(key string) string {
	parts := strings.Split(key, "-")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// getChecksumFromEvent extracts the checksum from a NIP-94 event
func getChecksumFromEvent(event nostr.Event) string {
	for _, tag := range event.Tags {
		if len(tag) > 1 && tag[0] == "x" {
			return tag[1]
		}
	}
	return ""
}
