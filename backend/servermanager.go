package backend

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dweymouth/go-jellyfin"
	"github.com/dweymouth/supersonic/backend/mediaprovider"
	jellyfinMP "github.com/dweymouth/supersonic/backend/mediaprovider/jellyfin"
	subsonicMP "github.com/dweymouth/supersonic/backend/mediaprovider/subsonic"
	"github.com/dweymouth/supersonic/res"
	"github.com/google/uuid"
	"github.com/supersonic-app/go-subsonic/subsonic"
	"github.com/zalando/go-keyring"
)

type ServerManager struct {
	LoggedInUser string
	ServerID     uuid.UUID
	Server       mediaprovider.MediaProvider

	useKeyring        bool
	storePasswords    bool
	credentialFile    string
	credentialFileMu  sync.Mutex
	prefetchCoverCB   func(string)
	appName           string
	appVersion        string
	config            *Config
	onServerConnected []func(*ServerConfig)
	onLogout          []func()
}

var ErrUnreachable = errors.New("server is unreachable")

func NewServerManager(appName, appVersion string, config *Config, configDir string, storePasswords, useKeyring bool) *ServerManager {
	return &ServerManager{
		appName:        appName,
		appVersion:     appVersion,
		config:         config,
		useKeyring:     useKeyring,
		storePasswords: storePasswords,
		credentialFile: filepath.Join(configDir, "credentials.json"),
	}
}

func (s *ServerManager) SetPrefetchAlbumCoverCallback(cb func(string)) {
	s.prefetchCoverCB = cb
	if s.Server != nil {
		s.Server.SetPrefetchCoverCallback(cb)
	}
}

func (s *ServerManager) ConnectToServer(conf *ServerConfig, password string) error {
	cli, err := s.connect(conf.ServerConnection, password)
	if err != nil {
		return err
	}
	s.Server = cli.MediaProvider()
	s.Server.SetPrefetchCoverCallback(s.prefetchCoverCB)
	s.LoggedInUser = conf.Username
	s.ServerID = conf.ID
	s.SetDefaultServer(s.ServerID)
	for _, cb := range s.onServerConnected {
		cb(conf)
	}
	return nil
}

func (s *ServerManager) TestConnectionAndAuth(
	ctx context.Context, connection ServerConnection, password string,
) error {
	err := ErrUnreachable
	done := make(chan bool)
	go func() {
		_, err = s.connect(connection, password)
		close(done)
	}()
	select {
	case <-ctx.Done():
		return err
	case <-done:
		return err
	}
}

func (s *ServerManager) GetDefaultServer() *ServerConfig {
	for _, s := range s.config.Servers {
		if s.Default {
			return s
		}
	}
	if len(s.config.Servers) > 0 {
		return s.config.Servers[0]
	}
	return nil
}

func (s *ServerManager) SetDefaultServer(serverID uuid.UUID) {
	var found bool
	for _, s := range s.config.Servers {
		f := s.ID == serverID
		if f {
			found = true
		}
		s.Default = f
	}
	if !found && len(s.config.Servers) > 0 {
		s.config.Servers[0].Default = true
	}
}

func (s *ServerManager) AddServer(nickname string, connection ServerConnection) *ServerConfig {
	sc := &ServerConfig{
		ID:               uuid.New(),
		Nickname:         nickname,
		ServerConnection: connection,
	}
	s.config.Servers = append(s.config.Servers, sc)
	return sc
}

func (s *ServerManager) DeleteServer(serverID uuid.UUID) {
	s.deleteServerPassword(serverID)
	newServers := make([]*ServerConfig, 0, len(s.config.Servers)-1)
	for _, s := range s.config.Servers {
		if s.ID != serverID {
			newServers = append(newServers, s)
		}
	}
	s.config.Servers = newServers
}

func (s *ServerManager) Logout(deletePassword bool) {
	if s.Server != nil {
		if deletePassword {
			s.deleteServerPassword(s.ServerID)
		}
		for _, cb := range s.onLogout {
			cb()
		}
		s.Server = nil
		s.LoggedInUser = ""
		s.ServerID = uuid.UUID{}
	}
}

func (s *ServerManager) deleteServerPassword(serverID uuid.UUID) {
	if !s.storePasswords {
		return
	}
	if s.useKeyring {
		_ = keyring.Delete(s.appName, serverID.String())
	}
	_ = s.deleteFilePassword(serverID)
}

// Sets a callback that is invoked when a server is connected to.
func (s *ServerManager) OnServerConnected(cb func(*ServerConfig)) {
	s.onServerConnected = append(s.onServerConnected, cb)
}

// Sets a callback that is invoked when the user logs out of a server.
func (s *ServerManager) OnLogout(cb func()) {
	s.onLogout = append(s.onLogout, cb)
}

func (s *ServerManager) GetServerPassword(serverID uuid.UUID) (string, error) {
	if !s.storePasswords {
		return "", errors.New("password storage not enabled")
	}
	if s.useKeyring {
		if password, err := keyring.Get(s.appName, serverID.String()); err == nil {
			return password, nil
		} else {
			log.Printf("OS keyring unavailable, using local credential store: %v", err)
		}
	}
	return s.getFilePassword(serverID)
}

func (s *ServerManager) SetServerPassword(server *ServerConfig, password string) error {
	if !s.storePasswords {
		return errors.New("password storage not enabled")
	}
	if s.useKeyring {
		if err := keyring.Set(s.appName, server.ID.String(), password); err == nil {
			_ = s.deleteFilePassword(server.ID)
			return nil
		} else {
			log.Printf("OS keyring unavailable, using local credential store: %v", err)
		}
	}
	return s.setFilePassword(server.ID, password)
}

func (s *ServerManager) readFilePasswords() (map[string]string, error) {
	passwords := make(map[string]string)
	data, err := os.ReadFile(s.credentialFile)
	if errors.Is(err, os.ErrNotExist) {
		return passwords, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &passwords); err != nil {
		return nil, fmt.Errorf("read saved credentials: %w", err)
	}
	return passwords, nil
}

func (s *ServerManager) writeFilePasswords(passwords map[string]string) error {
	tmp, err := os.CreateTemp(filepath.Dir(s.credentialFile), ".credentials-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := json.NewEncoder(tmp).Encode(passwords); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.credentialFile)
}

func (s *ServerManager) getFilePassword(serverID uuid.UUID) (string, error) {
	s.credentialFileMu.Lock()
	defer s.credentialFileMu.Unlock()
	passwords, err := s.readFilePasswords()
	if err != nil {
		return "", err
	}
	password, ok := passwords[serverID.String()]
	if !ok {
		return "", errors.New("no saved password")
	}
	return password, nil
}

func (s *ServerManager) setFilePassword(serverID uuid.UUID, password string) error {
	s.credentialFileMu.Lock()
	defer s.credentialFileMu.Unlock()
	passwords, err := s.readFilePasswords()
	if err != nil {
		return err
	}
	passwords[serverID.String()] = password
	return s.writeFilePasswords(passwords)
}

func (s *ServerManager) deleteFilePassword(serverID uuid.UUID) error {
	s.credentialFileMu.Lock()
	defer s.credentialFileMu.Unlock()
	passwords, err := s.readFilePasswords()
	if err != nil {
		return err
	}
	delete(passwords, serverID.String())
	if len(passwords) == 0 {
		err := os.Remove(s.credentialFile)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return s.writeFilePasswords(passwords)
}

func (s *ServerManager) connect(connection ServerConnection, password string) (mediaprovider.Server, error) {
	var cli, altCli mediaprovider.Server
	timeout := time.Second * time.Duration(s.config.Application.RequestTimeoutSeconds)

	if connection.ServerType == ServerTypeJellyfin {
		connection.Hostname = NormalizeJellyfinURL(connection.Hostname)
		connection.AltHostname = NormalizeJellyfinURL(connection.AltHostname)
	} else {
		connection.Hostname = NormalizeServerURL(connection.Hostname)
		connection.AltHostname = NormalizeServerURL(connection.AltHostname)
	}

	if connection.ServerType == ServerTypeJellyfin {
		client, err := jellyfin.NewClient(connection.Hostname, res.AppName, res.AppVersion, jellyfin.WithTimeout(timeout))
		if err != nil {
			log.Printf("error creating Jellyfin client: %s", err.Error())
			return nil, err
		}
		s.checkSetInsecureSkipVerify(connection.SkipSSLVerify, client.HTTPClient)
		cli = &jellyfinMP.JellyfinServer{
			Client: *client,
		}

		if connection.AltHostname != "" {
			altClient, err := jellyfin.NewClient(connection.AltHostname, res.AppName, res.AppVersion, jellyfin.WithTimeout(timeout))
			if err != nil {
				log.Printf("error creating Jellyfin alternative client: %s", err.Error())
				return nil, err
			}
			s.checkSetInsecureSkipVerify(connection.SkipSSLVerify, altClient.HTTPClient)
			altCli = &jellyfinMP.JellyfinServer{
				Client: *altClient,
			}
		}
	} else {
		ua := fmt.Sprintf("%s/%s", s.appName, s.appVersion)
		cli = &subsonicMP.SubsonicServer{
			Client: subsonic.Client{
				UserAgent:    ua,
				Client:       &http.Client{Timeout: timeout},
				BaseUrl:      connection.Hostname,
				User:         connection.Username,
				PasswordAuth: connection.LegacyAuth,
				ClientName:   res.AppName,
				UseJSON:      true,
			},
		}
		s.checkSetInsecureSkipVerify(connection.SkipSSLVerify, cli.(*subsonicMP.SubsonicServer).Client.Client)
		altCli = &subsonicMP.SubsonicServer{
			Client: subsonic.Client{
				UserAgent:    ua,
				Client:       &http.Client{Timeout: timeout},
				BaseUrl:      connection.AltHostname,
				User:         connection.Username,
				PasswordAuth: connection.LegacyAuth,
				ClientName:   res.AppName,
				UseJSON:      true,
			},
		}
		s.checkSetInsecureSkipVerify(connection.SkipSSLVerify, altCli.(*subsonicMP.SubsonicServer).Client.Client)
	}

	// struct to return hostname type in isAlt and connection success on err
	type pingResult struct {
		isAlt bool
		err   error
	}
	pingChan := make(chan pingResult, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pingFunc := func(delay time.Duration, cli mediaprovider.Server, isAlt bool) {
		// delay before connecting or exit if already cancelled
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}

		resp := cli.Login(connection.Username, password)
		if resp.Error != nil && !resp.IsAuthError {
			return
		}

		// return result or exit if already cancelled
		select {
		case pingChan <- pingResult{isAlt: isAlt, err: resp.Error}:
		case <-ctx.Done():
		}
	}
	go pingFunc(0, cli, false)
	if connection.AltHostname != "" {
		go pingFunc(333*time.Millisecond, altCli, true) // give primary hostname ping a head start
	}

	select {
	case <-ctx.Done():
		return nil, ErrUnreachable
	case res := <-pingChan:
		if res.isAlt {
			return altCli, res.err
		}
		return cli, res.err
	}
}

func (s *ServerManager) checkSetInsecureSkipVerify(skip bool, cli *http.Client) {
	if skip {
		cli.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
}

func (a *ServerManager) GetServer() mediaprovider.MediaProvider {
	return a.Server
}

// NormalizeServerURL applies common normalization to a server URL:
// prepends "http://" if no scheme is present, then strips trailing slashes.
func NormalizeServerURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	rawURL = strings.TrimRight(rawURL, "/")
	return rawURL
}

// NormalizeJellyfinURL applies common normalization then additionally strips
// known Jellyfin web UI path suffixes (/web/index.html and /web).
func NormalizeJellyfinURL(rawURL string) string {
	rawURL = NormalizeServerURL(rawURL)
	if strings.HasSuffix(rawURL, "/web/index.html") {
		rawURL = strings.TrimSuffix(rawURL, "/web/index.html")
	} else if strings.HasSuffix(rawURL, "/web") {
		rawURL = strings.TrimSuffix(rawURL, "/web")
	}
	return rawURL
}
