package main

import (
	"bytes"
	"crypto/des"
	"fmt"
	"strings"
)

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

func rightPad(s, char string, length int) string {
	padding := length - len(s)
	pad := strings.Repeat(char, padding)
	return s + pad
}

func desECBEncrypt(plaintext string, key string) (string, error) {
	keyBytes := []byte(rightPad(key, "0", 24))
	data := pkcs7Pad([]byte(plaintext), des.BlockSize)

	block, err := des.NewTripleDESCipher(keyBytes)
	if err != nil {
		return "", err
	}

	blockSize := block.BlockSize()
	encrypted := make([]byte, len(data))

	// Encrypt each block, like ECB mode
	for i := 0; i < len(data); i += blockSize {
		block.Encrypt(encrypted[i:i+blockSize], data[i:i+blockSize])
	}

	return fmt.Sprintf("%X", encrypted), nil
}
