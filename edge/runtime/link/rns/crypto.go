package rns

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// TokenOverhead is what a token adds to its plaintext before padding: IV and HMAC.
const TokenOverhead = 48

// A token is Fernet without its version and timestamp fields: IV, AES-128-CBC ciphertext of the
// PKCS#7-padded plaintext, and an HMAC-SHA256 over both. The 32-byte key splits into the HMAC key
// (first half) and the AES key (second half).

func tokenEncrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("rns: token key must be 32 bytes")
	}
	signingKey, encryptionKey := key[:16], key[16:]
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	signed := append(iv, ciphertext...)
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(signed)
	return mac.Sum(signed), nil
}

func tokenDecrypt(key, token []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("rns: token key must be 32 bytes")
	}
	if len(token) <= 32+aes.BlockSize || (len(token)-32-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, errors.New("rns: token has an invalid length")
	}
	signingKey, encryptionKey := key[:16], key[16:]
	body, received := token[:len(token)-32], token[len(token)-32:]
	mac := hmac.New(sha256.New, signingKey)
	mac.Write(body)
	if !hmac.Equal(received, mac.Sum(nil)) {
		return nil, errors.New("rns: token HMAC is invalid")
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, err
	}
	iv, ciphertext := body[:aes.BlockSize], body[aes.BlockSize:]
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext, aes.BlockSize)
}

func deriveKey(sharedSecret, salt []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, sharedSecret, salt, "", 32)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	n := blockSize - len(data)%blockSize
	return append(append([]byte{}, data...), bytes.Repeat([]byte{byte(n)}, n)...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("rns: invalid padding")
	}
	n := int(data[len(data)-1])
	if n == 0 || n > blockSize || n > len(data) {
		return nil, errors.New("rns: invalid padding")
	}
	for _, b := range data[len(data)-n:] {
		if int(b) != n {
			return nil, errors.New("rns: invalid padding")
		}
	}
	return data[:len(data)-n], nil
}

func fullHash(data ...[]byte) []byte {
	h := sha256.New()
	for _, d := range data {
		h.Write(d)
	}
	return h.Sum(nil)
}

func truncatedHash(data ...[]byte) []byte {
	return fullHash(data...)[:TruncatedHashLength]
}
