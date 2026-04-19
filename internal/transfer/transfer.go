package transfer

import (
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Target struct {
	User     string
	Addr     string
	Port     int
	Identity string
}

type Client struct {
	ssh  *ssh.Client
	sftp *sftp.Client
}

func Dial(t Target) (*Client, error) {
	auth, err := buildAuth(t.Identity)
	if err != nil {
		return nil, err
	}
	hk, err := hostKeyCallback()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            auth,
		HostKeyCallback: hk,
		Timeout:         10 * time.Second,
	}
	addr := net.JoinHostPort(t.Addr, strconv.Itoa(t.Port))
	sc, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	fc, err := sftp.NewClient(sc)
	if err != nil {
		sc.Close()
		return nil, fmt.Errorf("sftp: %w", err)
	}
	return &Client{ssh: sc, sftp: fc}, nil
}

func (c *Client) Close() {
	if c.sftp != nil {
		c.sftp.Close()
	}
	if c.ssh != nil {
		c.ssh.Close()
	}
}

func (c *Client) MkdirAll(remoteDir string) error {
	return c.sftp.MkdirAll(remoteDir)
}

func (c *Client) WriteFile(remotePath string, data []byte) error {
	f, err := c.sftp.Create(remotePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func (c *Client) Upload(remoteDir, filename string, data []byte) (string, error) {
	if err := c.MkdirAll(remoteDir); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", remoteDir, err)
	}
	p := path.Join(remoteDir, filename)
	if err := c.WriteFile(p, data); err != nil {
		return "", fmt.Errorf("write %s: %w", p, err)
	}
	return p, nil
}

func buildAuth(identity string) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	if identity != "" {
		p := expandHome(identity)
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", p, err)
		}
		signer, err := ssh.ParsePrivateKey(b)
		if err != nil {
			return nil, fmt.Errorf("parse key %s: %w", p, err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}

	if len(methods) == 0 {
		return nil, fmt.Errorf("no ssh auth available: set identity in config or run ssh-agent")
	}
	return methods, nil
}

func hostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	kh := home + string(os.PathSeparator) + ".ssh" + string(os.PathSeparator) + "known_hosts"
	if _, err := os.Stat(kh); err != nil {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	return knownhosts.New(kh)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return p
}
