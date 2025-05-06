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
	ListenForNIP94Events(j.configManager)
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

func ListenForNIP94Events(configManager *config_manager.ConfigManager) {
	log.Println("Starting to listen for NIP-94 events")
	ctx := context.Background()
	relayPool := nostr.NewSimplePool(ctx)
	eventChan := make(chan *nostr.Event, 1000)

	config, err := configManager.LoadConfig()
	if err != nil {
		log.Printf("Failed to load config: %v", err)
		return
	}

	for {
		var wg sync.WaitGroup
		for _, relayURL := range config.Relays {
			wg.Add(1)
			go func(relayURL string) {
				defer wg.Done()
				retryDelay := 5 * time.Second
				for {
					fmt.Printf("Connecting to relay: %s\n", relayURL)
					relay, err := relayPool.EnsureRelay(relayURL)
					if err != nil {
						// log.Printf("Failed to connect to relay %s: %v. Retrying in %v...", relayURL, err, retryDelay)
						time.Sleep(retryDelay)
						retryDelay *= 2
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
			//log.Println("Entering event processing loop")
			select {
				case event, ok := <-eventChan:
				//log.Printf("Received event from channel: %+v", event)
				if !ok {
					log.Println("eventChan closed, stopping event processing")
					return
				}
				totalEvents++
				//log.Printf("Total events: %d", totalEvents)
				if !contains(config.TrustedMaintainers, event.PubKey) {
					untrustedEventCount++
					// log.Printf("Untrusted event count: %d", untrustedEventCount)
					continue
				}

				trustedEventCount++
				// log.Printf("Trusted event count: %d", trustedEventCount)
				ok, err := event.CheckSignature()
				if err != nil || !ok {
					// log.Printf("Invalid signature for NIP-94 event %s: %v", event.ID, err)
					continue
				}

				packageURL, versionStr, arch, branch, filename, timestamp, releaseChannel, err := parseNIP94Event(*event)
				log.Printf("Parsed NIP-94 event: URL=%s, Version=%s, Arch=%s, Branch=%s, Filename=%s, Timestamp=%d, ReleaseChannel=%s, Err=%v",
					packageURL, versionStr, arch, branch, filename, timestamp, releaseChannel, err)
				if err != nil {
					if strings.Contains(err.Error(), "missing required tag 'release_channel'") {
						// log.Printf("Skipping NIP-94 event due to missing 'release_channel' tag: %v", err)
					} else {
						log.Printf("Error parsing NIP-94 event: %v", err)
					}
					continue
				}

				log.Printf("Debug ping!")

				//installConfig, err := configManager.LoadInstallConfig()
				//if err != nil {
				//	log.Printf("Error loading install config: %v", err)
				//	continue
				//}

				// Release channel from currently installed package
				releaseChannelFromConfigManager, err := configManager.GetReleaseChannel()
				if err != nil {
					log.Printf("Error getting release channel: %v", err)
					continue
				}
				log.Printf("Release channel from event: %s, from config: %s", releaseChannel, releaseChannelFromConfigManager)
				if releaseChannel != releaseChannelFromConfigManager {
					log.Printf("Skipping event due to release channel mismatch")
					continue // Skip if release of the currently installed file channel doesn't match the release channel of the incoming nostr event
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

				timestampConfig, err := configManager.GetTimestamp()
				if err != nil {
					log.Printf("Error getting timestamp: %v", err)
					continue
				}
				if timestamp > timestampConfig {
					// fmt.Printf("Received event from channel: ID=%s, URL=%s, Version=%s, Filename=%s, Timestamp=%d",
					// 	event.ID, packageURL, versionStr, filename, timestamp)
					rightTimeKeys = append(rightTimeKeys, key)
				}

				v, err := configManager.GetVersion()
				if err != nil {
					log.Printf("Error getting version: %v", err)
					continue
				}
				if isNewerVersion(versionStr, timestamp, v) {
					rightVersionKeys = append(rightVersionKeys, key)
				}

				branchFromNIP94Event, err := configManager.GetBranch()
				if err != nil {
					log.Printf("Error getting branch: %v", err)
					continue
				}
				if branch == branchFromNIP94Event {
					rightBranchKeys = append(rightBranchKeys, key)
				}

				archFromFilesystem, err := config_manager.GetArchitecture()
				if err != nil {
					log.Printf("Error getting architecture: %v", err)
					continue
				}
				if arch == archFromFilesystem {
					rightArchKeys = append(rightArchKeys, key)
				}

				intersection := intersect(rightTimeKeys, rightBranchKeys, rightArchKeys, rightVersionKeys)
				if len(intersection) > 0 && !isTimerActive {
					fmt.Printf("Started the timer\n")
					timer.Reset(10 * time.Second)
					isTimerActive = true
					//fmt.Printf("Started the timer, NIP-94 timestamp: %d, config timestamp: %d\n", timestamp, j.currentTimestamp)
					//fmt.Printf("Current timestamp %d, current version %s\n", j.currentTimestamp, j.currentVersion.String())
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
				_, versionStr, _, _, _, _, releaseChannel, err := parseNIP94Event(*event)
				if err != nil {
					log.Printf("Error parsing NIP-94 event %s: %v", event.ID, err)
					timer.Stop()
					isTimerActive = false
					return
				}

				fmt.Printf("Newer package version available: %s\n", versionStr)
				checksum := getChecksumFromEvent(*latestPackageEvent.event)
				pkgPath, pkg, err := DownloadPackage(configManager, latestPackageEvent.packageURL, checksum)
				if err != nil {
					log.Printf("Error downloading package: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}
				err = verifyPackageChecksum(pkg, *event)
				if err != nil {
					log.Printf("Error verifying package checksum: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}
				config, err := configManager.LoadConfig()
				if err != nil {
					log.Printf("Error loading config: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}
				config.NIP94EventID = event.ID
				err = configManager.SaveConfig(config)
				if err != nil {
					log.Printf("Error updating config with NIP94 event ID: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}

				installConfig, err := configManager.LoadInstallConfig()
				if err != nil {
					log.Printf("Error loading install config: %v", err)
					timer.Stop()
					isTimerActive = false
					return
				}
				installConfig.PackagePath = pkgPath
				installConfig.ReleaseChannel = releaseChannel
				err = configManager.SaveInstallConfig(installConfig)
				if err != nil {
					log.Printf("Error updating install config with package path: %v", err)
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

func DownloadPackage(configManager *config_manager.ConfigManager, url string, checksum string) (string, []byte, error) {
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

	// Update DownloadTimestamp in InstallConfig
	installConfig, err := configManager.LoadInstallConfig()
	if err != nil {
		log.Printf("Error loading install config: %v", err)
		return tmpFile.Name(), pkg, err
	}
	currentTime := time.Now().Unix()
	installConfig.DownloadTimestamp = currentTime
	err = configManager.SaveInstallConfig(installConfig)
	if err != nil {
		log.Printf("Error saving install config with DownloadTimestamp: %v", err)
		return tmpFile.Name(), pkg, err
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
func parseNIP94Event(event nostr.Event) (string, string, string, string, string, int64, string, error) {
	requiredTags := []string{"url", "version", "arch", "branch", "filename", "release_channel"}
	tagMap := make(map[string]string)

	for _, tag := range event.Tags {
		if len(tag) > 0 && len(tag) > 1 {
			tagMap[tag[0]] = tag[1]
		}
	}

	// Check if all required tags are present
	for _, tag := range requiredTags {
		if _, ok := tagMap[tag]; !ok {
			return "", "", "", "", "", 0, "", fmt.Errorf("invalid NIP-94 event: missing required tag '%s'", tag)
		}
	}

	url := tagMap["url"]
	version := tagMap["version"]
	arch := tagMap["arch"]
	branch := tagMap["branch"]
	filename := tagMap["filename"]
	timestamp := int64(event.CreatedAt)

	if url == "" || version == "" || timestamp == 0 {
		return "", "", "", "", "", 0, "", fmt.Errorf("invalid NIP-94 event: missing required tags")
	}

	releaseChannel := tagMap["release_channel"]
	return url, version, arch, branch, filename, timestamp, releaseChannel, nil
}

func isNewerVersion(newVersion string, newTimestamp int64, currentVersion *version.Version) bool {
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
	return newVersionObj.GreaterThan(cleanedCurrentVersionObj)
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
