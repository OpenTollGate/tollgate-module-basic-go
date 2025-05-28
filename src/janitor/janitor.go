package janitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/hashicorp/go-version"
	"github.com/nbd-wtf/go-nostr"
	"strconv"
)

type Janitor struct {
	configManager *config_manager.ConfigManager
}

func NewJanitor(configManager *config_manager.ConfigManager) (*Janitor, error) {
	return &Janitor{
		configManager: configManager,
	}, nil
}

func (j *Janitor) ListenForNIP94Events() {
	j.listenForNIP94Events()
}

type packageEvent struct {
	event      *nostr.Event
	packageURL string
}

// Helper functions to get installed version and architecture
func getInstalledVersion() (string, error) {
	_, err := exec.LookPath("opkg")
	if err != nil {
		return "0.0.1+1cac608", nil // Default version if opkg is not found
	}
	cmd := exec.Command("opkg", "list-installed", "tollgate-basic")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get installed version: %w", err)
	}
	outputStr := strings.TrimSpace(string(output))
	parts := strings.Split(outputStr, " - ")
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected output format: %s", outputStr)
	}
	return parts[1], nil
}

var subscriptionSemaphore = make(chan struct{}, 5) // Allow up to 5 concurrent relay subscriptions

func rateLimitedSubscribe(relay *nostr.Relay, ctx context.Context, filters []nostr.Filter) (*nostr.Subscription, error) {
    subscriptionSemaphore <- struct{}{}
    defer func() { <-subscriptionSemaphore }()
    
    return relay.Subscribe(ctx, filters)
}

func (j *Janitor) listenForNIP94Events() {
	log.Println("Starting to listen for NIP-94 events")
	ctx := context.Background()
	eventChan := make(chan *nostr.Event, 1000)

	mainConfig, err := j.configManager.LoadConfig()
	if err != nil {
		log.Printf("Failed to load config: %v", err)
		return
	}

	for {
		var wg sync.WaitGroup
		for _, relayURL := range mainConfig.Relays {
			wg.Add(1)
			go func(relayURL string) {
				defer wg.Done()
				subscriptionSemaphore <- struct{}{}              // Acquire semaphore
				defer func() { <-subscriptionSemaphore }() // Release semaphore

				retryDelay := 5 * time.Second
				for {
					fmt.Printf("Connecting to relay: %s\n", relayURL)
					relay, err := j.configManager.RelayPool.EnsureRelay(relayURL)
					if err != nil || relay == nil {
						log.Printf("Failed to ensure relay %s: %v", relayURL, err)
						continue
					}
					if err != nil {
						time.Sleep(retryDelay)
						retryDelay *= 2
						continue
					}
					fmt.Printf("Connected to relay: %s\n", relayURL)

					sub, err := rateLimitedSubscribe(relay, ctx, []nostr.Filter{
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
		rightTimeKeys := make([]string, 0)
		var already_printed bool = false
		rightArchKeys := make([]string, 0)
		rightVersionKeys := make([]string, 0)

		debounceTimer := time.NewTimer(10 * time.Second)
		debounceTimer.Stop()
		isTimerActive := false
		fmt.Println("Starting event processing loop")
		for {
			select {
			case event, ok := <-eventChan:
				if !ok {
					log.Println("eventChan closed, stopping event processing")
					return
				}
				if !contains(mainConfig.TrustedMaintainers, event.PubKey) {
					continue
				}

				ok, err := event.CheckSignature()
				if err != nil || !ok {
					continue
				}

				packageURL, versionStr, arch, filename, timestamp, releaseChannel, err := parseNIP94Event(*event)
				// log.Printf("Parsed NIP-94 event: URL=%s, Version=%s, Arch=%s, Filename=%s, Timestamp=%d, ReleaseChannel=%s, Err=%v",
				// 	packageURL, versionStr, arch, filename, timestamp, releaseChannel, err)
				if err != nil {
					// if strings.Contains(err.Error(), "missing required tag 'release_channel'") {
					// } else {
					// 	log.Printf("Error parsing NIP-94 event: %v", err)
					// }
					continue
				}

				releaseChannelFromConfigManager, err := j.configManager.GetReleaseChannel()
				if err != nil {
					log.Printf("Error getting release channel: %v", err)
					continue
				}
				// log.Printf("Release channel from event: %s, from config: %s", releaseChannel, releaseChannelFromConfigManager)
				if releaseChannel != releaseChannelFromConfigManager {
					// log.Printf("Skipping event due to release channel mismatch")
					continue
				}
				key := fmt.Sprintf("%s-%s", filename, versionStr)
				ok = eventMap[key] != nil
				if ok {
					//collisionCount++
				} else {
					eventMap[key] = &packageEvent{
						event:      event,
						packageURL: packageURL,
					}
				}

				timestampConfig, err := j.configManager.GetTimestamp()
				if err != nil {
					log.Printf("Error getting timestamp: %v", err)
					continue
				}
				if timestamp > timestampConfig {
					//log.Printf("Found righttime: %s", key)
					rightTimeKeys = append(rightTimeKeys, key)
				}

				vStr, err := j.configManager.GetVersion()
				if err != nil {
					log.Printf("Error getting version: %v", err)
					continue
				}

				releaseChannel, err = j.configManager.GetReleaseChannel()
				if err != nil {
					log.Printf("Error getting release channel: %v", err)
					continue
				}
				if isNewerVersion(versionStr, vStr, releaseChannel) {
					//log.Printf("Found rightversion: %s", key)
					rightVersionKeys = append(rightVersionKeys, key)
				}

				archFromFilesystem, err := j.configManager.GetArchitecture()
				if err != nil {
					log.Printf("Error getting architecture: %v", err)
					continue
				}
				if arch == archFromFilesystem {
					//fmt.Printf("Received event: %+v\n", event)
					//log.Printf("Found rightarch: %s", key)
					rightArchKeys = append(rightArchKeys, key)
				}

				intersection := intersect(rightTimeKeys, rightArchKeys, rightVersionKeys)
				if len(intersection) > 0 && !isTimerActive {
					fmt.Printf("Started the timer\n")
					debounceTimer.Reset(10 * time.Second)
					isTimerActive = true
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
					printList("Right Arch Keys", rightArchKeys)
					printList("Right Version Keys", rightVersionKeys)
					already_printed = true
				}

			case <-debounceTimer.C:
				log.Println("Timeout reached, checking for new versions")

				intersection := intersect(rightTimeKeys, rightArchKeys, rightVersionKeys)
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
					debounceTimer.Stop()
					isTimerActive = false
					return
				}

				event := latestPackageEvent.event
				_, versionStr, _, _, _, _, err := parseNIP94Event(*event)
				if err != nil {
					log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
					debounceTimer.Stop()
					isTimerActive = false
					return
				}

				fmt.Printf("Newer package version available: %s\n", versionStr)
				checksum := getChecksumFromEvent(*latestPackageEvent.event)
				pkgPath, pkg, err := DownloadPackage(j, latestPackageEvent.packageURL, checksum)
				if err != nil {
					log.Printf("Error downloading package: %v", err)
					debounceTimer.Stop()
					isTimerActive = false
					return
				}
				err = verifyPackageChecksum(pkg, *event)
				if err != nil {
					log.Printf("Error verifying package checksum: %v", err)
					debounceTimer.Stop()
					isTimerActive = false
					return
				}
				config, err := j.configManager.LoadConfig()
				if err != nil {
					log.Printf("Error loading config: %v", err)
					debounceTimer.Stop()
					isTimerActive = false
					return
				}
				config.CurrentInstallationID = event.ID
				err = j.configManager.SaveConfig(config)
				if err != nil {
					log.Printf("Error updating config with NIP94 event ID: %v", err)
					debounceTimer.Stop()
					isTimerActive = false
					return
				}

				installConfig, err := j.configManager.LoadInstallConfig()
				if err != nil {
					log.Printf("Error loading install config: %v", err)
					debounceTimer.Stop()
					isTimerActive = false
					return
				}
				installConfig.PackagePath = pkgPath
				err = j.configManager.SaveInstallConfig(installConfig)
				if err != nil {
					log.Printf("Error updating install config with package path: %v", err)
					debounceTimer.Stop()
					isTimerActive = false
					return
				}
				debounceTimer.Stop()
				isTimerActive = false
			}
		}
	}
}

func DownloadPackage(j *Janitor, url string, checksum string) (string, []byte, error) {
	filename := checksum + ".ipk"
	tmpFilePath := filepath.Join("/tmp/", filename)

	// Check if file already exists
	pkg, err := os.ReadFile(tmpFilePath)
	if err == nil {
		// Verify checksum if file exists
		event := nostr.Event{
			Tags: nostr.Tags{
				{"x", checksum},
			},
		}
		err = verifyPackageChecksum(pkg, event)
		if err == nil {
			fmt.Printf("Package %s already exists with correct checksum, skipping download\n", tmpFilePath)
			return tmpFilePath, pkg, nil
		} else {
			log.Printf("Existing package checksum verification failed: %v", err)
		}
	}

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

	pkg, err = os.ReadFile(tmpFile.Name())
	if err != nil {
		log.Printf("Error reading downloaded package: %v", err)
		return "", nil, err
	}

	fmt.Println("Package downloaded successfully to /tmp/")

	installConfig, err := j.configManager.LoadInstallConfig()
	if err != nil {
		log.Printf("Error loading install config: %v", err)
		return tmpFile.Name(), pkg, err
	}
	currentTime := time.Now().Unix()
	installConfig.DownloadTimestamp = currentTime
	err = j.configManager.SaveInstallConfig(installConfig)
	if err != nil {
		log.Printf("Error saving install config with DownloadTimestamp: %v", err)
		return tmpFile.Name(), pkg, err
	} else {
		fmt.Println("New package version is ready to be installed by cronjob")
	}

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

func verifyPackageChecksum(pkg []byte, event nostr.Event) error {
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

func isNetworkUnreachable(err error) bool {
	if err == nil {
		return false
	}
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
func parseNIP94Event(event nostr.Event) (string, string, string, string, int64, string, error) {
	requiredTags := []string{"url", "version", "architecture", "filename", "release_channel"}
	tagMap := make(map[string]string)

	for _, tag := range event.Tags {
		if len(tag) > 0 && len(tag) > 1 {
			tagMap[tag[0]] = tag[1]
		}
	}

	for _, tag := range requiredTags {
		if _, ok := tagMap[tag]; !ok {
			return "", "", "", "", 0, "", fmt.Errorf("invalid NIP-94 event: missing required tag '%s'", tag)
		}
	}

	url := tagMap["url"]
	version := tagMap["version"]
	arch := tagMap["architecture"]
	filename := tagMap["filename"]
	timestamp := int64(event.CreatedAt)

	if url == "" || version == "" || timestamp == 0 {
		return "", "", "", "", 0, "", fmt.Errorf("invalid NIP-94 event: missing required tags")
	}

	releaseChannel := tagMap["release_channel"]
	return url, version, arch, filename, timestamp, releaseChannel, nil
}

func isNewerVersion(newVersion string, currentVersion string, releaseChannel string) bool {

	//log.Printf("isNewerVersion: releaseChannel=%s, newVersion=%s, currentVersion=%s", releaseChannel, newVersion, currentVersion)
	if releaseChannel == "dev" {
		//log.Println("isNewerVersion: Processing dev release channel, newVersion=%s", newVersion)
		newVersionParts := strings.Split(newVersion, ".")
		if len(newVersionParts) != 3 {
			//log.Printf("isNewerVersion: Invalid new version format: %s", newVersion)
			return false
		}
		newCommits, err := strconv.Atoi(newVersionParts[1])
		if err != nil {
			log.Printf("Error converting new commits to integer: %v, newVersion=%s", err, newVersion)
			return false
		}

		currentVersionParts := strings.Split(currentVersion, ".")
		if len(currentVersionParts) != 3 {
			log.Printf("Invalid current version format: %s, newVersion=%s", currentVersion, newVersion)
			return false
		}

		if newVersionParts[0] != currentVersionParts[0] {
			//log.Printf("Major version mismatch: new=%s, current=%s, newVersion=%s", newVersionParts[0], currentVersionParts[0], newVersion)
			return false
		}

		newCommits, err = strconv.Atoi(newVersionParts[1])
		if err != nil {
			log.Printf("Error converting new commits to integer: %v, newVersion=%s", err, newVersion)
			return false
		}

		currentCommits, err := strconv.Atoi(currentVersionParts[1])
		if err != nil {
			log.Printf("Error converting current commits to integer: %v, newVersion=%s", err, newVersion)
			return false
		}

		// log.Printf("Comparing commits: newCommits=%d, currentCommits=%d, newVersion=%s", newCommits, currentCommits, newVersion)
		return newCommits > currentCommits
	} else {
		newVersionObj, err := version.NewVersion(newVersion)
		if err != nil {
			return false
		}
		cleanedCurrentVersionObj, err := version.NewVersion(currentVersion)
		if err != nil {
			return false
		}
		return newVersionObj.GreaterThan(cleanedCurrentVersionObj)
	}
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
