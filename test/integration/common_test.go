// +build integration

package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"

	"github.com/eggsampler/acme/v2"
)

func random_domain() string {
	var bytes [3]byte
	rand.Read(bytes[:])
	return hex.EncodeToString(bytes[:]) + ".com"
}

type client struct {
	acme.Account
	acme.Client
}

func makeClient() (*client, error) {
	c, err := acme.NewClient(os.Getenv("DIRECTORY"))
	if err != nil {
		return nil, fmt.Errorf("Error connecting to acme directory: %v", err)
	}
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("error creating private key: %v", err)
	}
	account, err := c.NewAccount(privKey, false, true, "mailto:example@letsencrypt.org")
	if err != nil {
		return nil, fmt.Errorf("error creating new account: %v", err)
	}
	return &client{account, c}, nil
}

func addHTTP01Response(token, keyAuthorization string) error {
	resp, err := http.Post("http://boulder:8055/add-http01", "",
		bytes.NewBufferString(fmt.Sprintf(`{
		"token": "%s",
		"content": "%s"
	}`, token, keyAuthorization)))
	if err != nil {
		return fmt.Errorf("adding http-01 response: %s", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("adding http-01 response: status %d", resp.StatusCode)
	}
	resp.Body.Close()
	return nil
}

func delHTTP01Response(token string) error {
	resp, err := http.Post("http://boulder:8055/del-http01", "",
		bytes.NewBufferString(fmt.Sprintf(`{
		"token": "%s"
	}`, token)))
	if err != nil {
		return fmt.Errorf("deleting http-01 response: %s", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deleting http-01 response: status %d", resp.StatusCode)
	}
	resp.Body.Close()
	return nil
}

type issuanceResult struct {
	acme.Order
	certs []*x509.Certificate
}

func authAndIssue(domains []string) (*issuanceResult, error) {
	c, err := makeClient()
	if err != nil {
		return nil, err
	}
	var ids []acme.Identifier
	for _, domain := range domains {
		ids = append(ids, acme.Identifier{Type: "dns", Value: domain})
	}
	order, err := c.Client.NewOrder(c.Account, ids)
	if err != nil {
		return nil, fmt.Errorf("making order: %s", err)
	}

	for _, authUrl := range order.Authorizations {
		auth, err := c.Client.FetchAuthorization(c.Account, authUrl)
		if err != nil {
			return nil, fmt.Errorf("fetching authorization at %s: %s", authUrl, err)
		}

		chal, ok := auth.ChallengeMap[acme.ChallengeTypeHTTP01]
		if !ok {
			return nil, fmt.Errorf("no HTTP challenge at %s", authUrl)
		}

		err = addHTTP01Response(chal.Token, chal.KeyAuthorization)
		if err != nil {
			return nil, fmt.Errorf("adding HTTP-01 response: %s", err)
		}
		defer delHTTP01Response(chal.Token)
		chal, err = c.Client.UpdateChallenge(c.Account, chal)
		if err != nil {
			return nil, fmt.Errorf("updating challenge: %s", err)
		}
	}

	csr, err := makeCSR(domains)
	if err != nil {
		return nil, err
	}

	order, err = c.Client.FinalizeOrder(c.Account, order, csr)
	if err != nil {
		return nil, fmt.Errorf("finalizing order: %s", err)
	}
	certs, err := c.Client.FetchCertificates(c.Account, order.Certificate)
	if err != nil {
		return nil, fmt.Errorf("fetching certificates: %s", err)
	}
	return &issuanceResult{order, certs}, nil
}

func makeCSR(domains []string) (*x509.CertificateRequest, error) {
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating certificate key: %s", err)
	}

	csrDer, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		SignatureAlgorithm: x509.ECDSAWithSHA256,
		PublicKeyAlgorithm: x509.ECDSA,
		PublicKey:          certKey.Public(),
		Subject:            pkix.Name{CommonName: domains[0]},
		DNSNames:           domains,
	}, certKey)
	if err != nil {
		return nil, fmt.Errorf("making csr: %s", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDer)
	if err != nil {
		return nil, fmt.Errorf("parsing csr: %s", err)
	}
	return csr, nil
}
