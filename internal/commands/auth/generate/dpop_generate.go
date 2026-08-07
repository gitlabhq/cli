package generate

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/commands/auth/generate/dpop"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/mcpannotations"
	"gitlab.com/gitlab-org/cli/internal/text"
)

type options struct {
	io                  *iostreams.IOStreams
	config              func() config.Config
	privateKeyLocation  string
	personalAccessToken string
	hostname            string
}

type PasswordReader interface {
	Read() ([]byte, error)
}

type ConsolePasswordReader struct{}

func (pr ConsolePasswordReader) Read() ([]byte, error) {
	return term.ReadPassword(int(os.Stdin.Fd()))
}

func NewCmdGenerate(f cmdutils.Factory) *cobra.Command {
	opts := &options{
		io:     f.IO(),
		config: f.Config,
	}
	cmd := &cobra.Command{
		Use:   "dpop-gen",
		Short: "Generate a DPoP (demonstrating-proof-of-possession) proof JWT. (EXPERIMENTAL)",
		Long: heredoc.Docf(`
		Generates a demonstrating-proof-of-possession (DPoP) proof JSON Web Token (JWT).
		DPoP cryptographically binds personal access tokens to their owners.

		Use the JWT with a personal access token (PAT) to authenticate to the GitLab API.
		glab signs the JWT with your SSH private key. The JWT remains valid for five
		minutes. After it expires, generate another one.

		Prerequisites:

		- An SSH key pair in RSA, ed25519, or ECDSA format.
		- [Enabled DPoP for your account](https://docs.gitlab.com/user/profile/personal_access_tokens/#use-dpop-with-personal-access-tokens).
		%s`, text.ExperimentalString),
		Example: heredoc.Doc(`
			# Generate a DPoP JWT using an SSH private key and a personal access token
			glab auth dpop-gen --private-key "~/.ssh/id_rsa" --pat "glpat-xxxxxxxxxxxxxxxxxxxx"

			# Generate a DPoP JWT without a PAT (uses the token from 'glab auth login')
			glab auth dpop-gen --private-key "~/.ssh/id_rsa"

			# Generate a DPoP JWT for a different GitLab instance
			glab auth dpop-gen --private-key "~/.ssh/id_rsa" --hostname "https://gitlab.com"
		`),
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			mcpannotations.Destructive: "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := opts.complete(); err != nil {
				return err
			}

			return opts.run()
		},
	}

	cmd.Flags().StringVarP(&opts.privateKeyLocation, "private-key", "p", "", "Location of the private SSH key on the local system.")
	cmd.Flags().StringVar(&opts.personalAccessToken, "pat", "", "Personal access token (PAT) to generate a DPoP proof for. Defaults to the token set with 'glab auth login'.")
	cmd.Flags().StringVarP(&opts.hostname, "hostname", "", "gitlab.com", "The hostname of the GitLab instance to authenticate with.")
	cobra.CheckErr(cmd.MarkFlagRequired("private-key"))

	return cmd
}

func (o *options) complete() error {
	if o.personalAccessToken == "" {
		cfg := o.config()

		token, err := cfg.Get(o.hostname, "token")
		if err != nil {
			return err
		}

		if token != "" {
			o.personalAccessToken = token
		} else {
			return fmt.Errorf("personal access token is required")
		}
	}

	return nil
}

func (o *options) run() error {
	privateKey, err := loadPrivateKey(o.privateKeyLocation, ConsolePasswordReader{})
	if err != nil {
		return err
	}

	proofString, err := generateDPoPProof(privateKey, o.personalAccessToken)
	if err != nil {
		return err
	}

	log.Println("DPoP Proof:", proofString)
	return nil
}

func generateDPoPProof(key crypto.PrivateKey, accessToken string) (string, error) {
	signingMethod, err := getSigningMethod(key)
	if err != nil {
		return "", err
	}

	hashedToken := sha256.Sum256([]byte(accessToken))
	base64UrlEncodedHash := base64.RawURLEncoding.EncodeToString(hashedToken[:])

	uuidObj, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}

	publicKey, err := getPublicKey(key)
	if err != nil {
		return "", err
	}

	sshPubKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return "", err
	}

	fingerprint := ssh.FingerprintSHA256(sshPubKey)

	now := time.Now()
	claims := &dpop.ProofTokenClaims{
		RegisteredClaims: &jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute * 5)),
			ID:        uuidObj.String(),
		},
		AccessTokenHash: base64UrlEncodedHash,
	}

	signer, ok := key.(crypto.Signer)
	if !ok {
		return "", fmt.Errorf("key type does not implement crypto.Signer")
	}

	jwk, err := dpop.Reflect(publicKey)
	if err != nil {
		return "", err
	}
	token := &jwt.Token{
		Header: map[string]any{
			"typ": "dpop+jwt",
			"alg": signingMethod.Alg(),
			"jwk": jwk,
			"kid": fingerprint,
		},
		Claims: claims,
		Method: signingMethod,
	}

	signedToken, err := token.SignedString(signer)
	if err != nil {
		return "", err
	}

	return signedToken, nil
}

func getSigningMethod(key crypto.PrivateKey) (jwt.SigningMethod, error) {
	var signingMethod jwt.SigningMethod
	switch key := key.(type) {
	case *rsa.PrivateKey:
		{
			if key.N.BitLen() < 2048 {
				// Minimum should be 2048 as per https://www.rfc-editor.org/rfc/rfc7518.html#section-3.3
				return nil, fmt.Errorf("RSA key size must be greater than 2048 bits")
			} else if key.N.BitLen() > 8192 {
				// Maximum should be 8192 as per https://docs.gitlab.com/user/ssh/#rsa-ssh-keys
				return nil, fmt.Errorf("RSA key size must be less than 8192 bits")
			}
			signingMethod = jwt.SigningMethodRS512
		}
	case *ed25519.PrivateKey:
		signingMethod = jwt.SigningMethodEdDSA
	default:
		return nil, fmt.Errorf("unsupported key type")
	}
	return signingMethod, nil
}

func getPublicKey(key crypto.PrivateKey) (crypto.PublicKey, error) {
	switch key := key.(type) {
	case *rsa.PrivateKey:
		return key.Public(), nil
	case *ed25519.PrivateKey:
		publicKey, ok := key.Public().(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("ed25519 private key did not return an ed25519 public key")
		}
		return publicKey, nil
	default:
		return nil, fmt.Errorf("unsupported key type")
	}
}

func loadPrivateKey(path string, passwordReader PasswordReader) (crypto.PrivateKey, error) {
	keyBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	privateKey, err := ssh.ParseRawPrivateKey(keyBytes)
	if err != nil {
		var passphraseMissingErr *ssh.PassphraseMissingError
		if errors.As(err, &passphraseMissingErr) {
			fmt.Println("SSH private key is encrypted. Enter your key's passphrase: ")
			passphrase, err := passwordReader.Read()
			if err != nil {
				return nil, err
			}

			privateKey, err = ssh.ParseRawPrivateKeyWithPassphrase(keyBytes, passphrase)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	pk, ok := privateKey.(crypto.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("parsed key %T does not satisfy crypto.PrivateKey", privateKey)
	}
	return pk, nil
}
