package aestcp

import (
	"io"

	"golang.org/x/crypto/curve25519"
)

func generateKey(rand io.Reader) (*[32]byte, *[32]byte, error) {
	privateKey, publicKey := new([32]byte), new([32]byte)
	_, err := io.ReadFull(rand, privateKey[:])
	if err != nil {
		return nil, nil, err
	}

	curve25519.ScalarBaseMult(publicKey, privateKey)
	return privateKey, publicKey, nil
}

func generateSharedSecret(privateKey, publicKey *[32]byte) *[32]byte {
	secret := new([32]byte)

	curve25519.ScalarMult(secret, privateKey, publicKey)
	return secret
}
