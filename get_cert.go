package main

import (
	"bytes"
	"fmt"
	"github.com/cloudtools/ssh-cert-authority/util"
	"github.com/codegangsta/cli"
	"golang.org/x/crypto/ssh"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func getCertFlags() []cli.Flag {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	configPath := home + "/.ssh_ca/requester_config.json"

	return []cli.Flag{
		cli.StringFlag{
			Name:  "environment",
			Value: "",
			Usage: "An environment name (e.g. prod)",
		},
		cli.StringFlag{
			Name:  "config-file",
			Value: configPath,
			Usage: "Path to config.json",
		},
		cli.BoolTFlag{
			Name:  "add-key",
			Usage: "When set automatically call ssh-add",
		},
	}
}

func getCert(c *cli.Context) {

	configPath := c.String("config-file")
	environment := c.String("environment")
	certRequestID := c.Args().First()

	allConfig := make(map[string]ssh_ca_util.RequesterConfig)
	err := ssh_ca_util.LoadConfig(configPath, &allConfig)
	wrongTypeConfig, err := ssh_ca_util.GetConfigForEnv(environment, &allConfig)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	config := wrongTypeConfig.(ssh_ca_util.RequesterConfig)

	getResp, err := http.Get(config.SignerUrl + "cert/requests/" + certRequestID)
	if err != nil {
		fmt.Println("Didn't get a valid response", err)
		os.Exit(1)
	}
	getRespBuf, err := ioutil.ReadAll(getResp.Body)
	if err != nil {
		fmt.Println("Error reading response body", err)
		os.Exit(1)
	}
	getResp.Body.Close()
	if getResp.StatusCode != 200 {
		fmt.Println("Error getting that request id:", string(getRespBuf))
		os.Exit(1)
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey(getRespBuf)
	if err != nil {
		fmt.Println("Trouble parsing response", err)
		os.Exit(1)
	}
	cert := pubKey.(*ssh.Certificate)
	secondsRemaining := int64(cert.ValidBefore) - int64(time.Now().Unix())
	if secondsRemaining < 1 {
		fmt.Println("This certificate has already expired.")
		os.Exit(1)
	}

	pubKeyPath, err := findKeyLocally(cert.Key)

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	pubKeyPath = strings.Replace(pubKeyPath, ".pub", "-cert.pub", 1)
	err = ioutil.WriteFile(pubKeyPath, getRespBuf, 0644)
	if err != nil {
		fmt.Printf("Couldn't write certificate file to %s: %s\n", pubKeyPath, err)
	}

	ssh_ca_util.PrintForInspection(*cert)
	if c.BoolT("add-key") {
		privKeyPath := strings.Replace(pubKeyPath, "-cert.pub", "", 1)
		cmd := exec.Command("ssh-add", "-t", fmt.Sprintf("%d", secondsRemaining), privKeyPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		err = cmd.Run()
		if err != nil {
			fmt.Println("Error in ssh-add")
			os.Exit(1)
		}
	}
}

func findKeyLocally(key ssh.PublicKey) (string, error) {
	sshDir := os.Getenv("HOME") + "/.ssh"
	dirEntries, err := ioutil.ReadDir(sshDir)
	if err != nil {
		return "", fmt.Errorf("Could not read your .ssh directory %s: %s\n", sshDir, err)
	}
	for idx := range dirEntries {
		entry := dirEntries[idx]
		if strings.HasSuffix(entry.Name(), ".pub") {
			pubKeyPath := sshDir + "/" + entry.Name()
			pubBuf, err := ioutil.ReadFile(pubKeyPath)
			if err != nil {
				fmt.Printf("Trouble reading public key %s: %s\n", pubKeyPath, err)
				continue
			}
			pubKey, _, _, _, err := ssh.ParseAuthorizedKey(pubBuf)
			if err != nil {
				fmt.Printf("Trouble parsing public key %s (might be unsupported format): %s\n", pubKeyPath, err)
				continue
			}
			if bytes.Equal(pubKey.Marshal(), key.Marshal()) {
				return pubKeyPath, nil
			}
		}
	}
	return "", fmt.Errorf("Couldn't find ssh key for cert.\n")
}
