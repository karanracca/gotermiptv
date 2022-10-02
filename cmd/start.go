package cmd

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ktr0731/go-fuzzyfinder"
	"github.com/spf13/cobra"
)

var M3U_URL = ""

type Channel struct {
	Title string
	URL   string
}

var Cast string

func init() {
	// Adding cast flag, to cast to chromecast
	startCmd.Flags().StringVarP(&Cast, "cast", "c", "", "Cast to chromecast")

	rootCmd.AddCommand(startCmd)
}

func play(cmd *exec.Cmd, done chan bool) {
	stdoutStderr, err := cmd.CombinedOutput()
	if err != nil {
		log.Panicf("unable to play stream with error: %v", err)
	}
	log.Printf("[vlc] %s\n", stdoutStderr)
	cmd.Wait()
	done <- true
}

func fetchAndStore(url, path string) error {
	res, err := http.Get(url)
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("fetching file failed with error %v", res.Status)
	}
	defer res.Body.Close()

	out, err := os.Create(path)
	if err != nil {
		return err
	}

	defer out.Close()

	io.Copy(out, res.Body)
	return nil
}

func checkM3uFile() (string, bool) {
	// Check if the m3u file is present
	matches, err := filepath.Glob(`*_channels.m3u`)
	if err != nil {
		log.Printf("file check failed with error: %v", err)
		return "", false
	}

	if len(matches) <= 0 {
		log.Printf("no m3u files found")
		return "", false
	} else if len(matches) == 1 {
		maxAgeString := strings.Split(matches[0], "_")[0]
		maxAge, err := strconv.ParseInt(maxAgeString, 10, 64)
		if err != nil {
			log.Printf("unable to parse file age %v", err)
			return "", false
		}

		current := time.Now().Unix()
		if current < maxAge {
			log.Printf("m3u file present %s", matches[0])
			return matches[0], true
		} else {
			log.Printf("m3u file stale %s", matches[0])
			return "", false
		}
	} else {
		log.Printf("multiple m3u files found %v", matches)
		return "", false
	}
}

func extractChannelList(path string) ([]Channel, error) {
	channelList := make([]Channel, 0)
	// read m3u file
	body, err := ioutil.ReadFile(path)
	if err != nil {
		return channelList, err
	}
	rawList := strings.Split(string(body), "#EXTINF:-1")

	for _, rawLine := range rawList[1:] {
		rawlineList := strings.Split(rawLine, "\r\n")
		if len(rawlineList) < 3 {
			log.Println("Invalid elements in list row")
		}

		groupTitleRegex := regexp.MustCompile("group-title=\"(.*)\",(.*)")
		match := groupTitleRegex.FindStringSubmatch(rawlineList[0])
		if len(match) < 2 {
			log.Println("Invalid elements in group title")
		}
		groupTitle := match[0]
		//groupDesc := match[1]

		channelList = append(channelList, Channel{groupTitle, rawlineList[1]})
	}

	return channelList, nil
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Starts a new session",
	Args:  cobra.ExactValidArgs(1),
	Run: func(cmd *cobra.Command, args []string) {

		M3U_URL = args[0]

		var m3uFile string

		fetchedM3uFile, ok := checkM3uFile()
		if ok {
			m3uFile = fetchedM3uFile
		} else {
			log.Printf("fetching m3u file...")
			maxAge := time.Now().Add(time.Hour * 24 * 7).Unix()
			filePath := fmt.Sprintf("%d_channels.m3u", maxAge)
			if err := fetchAndStore(M3U_URL, filePath); err != nil {
				log.Panicf("downloading m3u file failed with error: %v", err)
			}
			m3uFile = filePath
		}

		channels, err := extractChannelList(m3uFile)
		if err != nil {
			log.Panicf("extracting channels from m3u file failed with error: %v", err)
		}

		// Display channel list in a fuzzy finder
		idx, err := fuzzyfinder.Find(
			channels,
			func(i int) string {
				return channels[i].Title
			},
		)
		if err != nil {
			log.Panicf("fuzzy finder error: %v", err)
		}

		channelToPlay := channels[idx]
		// Play channel

		playerPath, err := exec.LookPath("vlc")
		if err != nil {
			log.Panicf("vlc does not exist in the path. Please install VLC player and add it to the path")
		}

		var playCmd *exec.Cmd
		if len(Cast) > 0 {
			fmt.Printf("casting to chromecast with ip 192.168.4.85")
			playCmd = exec.Command(playerPath, channelToPlay.URL, "--sout", "#chromecast", "--sout-chromecast-ip", "192.168.4.85")
		} else {
			playCmd = exec.Command(playerPath, channelToPlay.URL)
		}

		done := make(chan bool, 1)
		go play(playCmd, done)

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

		for {
			select {
			case <-done:
				log.Printf("closing player")
				return
			case <-sig:
				// Kill player process
				if err := playCmd.Process.Kill(); err != nil {
					log.Fatalf("failed to kill process with error: %v", err)
				}
				return
			}
		}
	},
}
