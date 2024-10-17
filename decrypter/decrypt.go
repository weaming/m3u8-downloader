package decrypter

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

func Decrypt(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	cbc := cipher.NewCBCDecrypter(block, iv)
	cbc.CryptBlocks(data, data)

	return PKCS7UnPadding(data)
}

func PKCS7UnPadding(origData []byte) ([]byte, error) {
	length := len(origData)
	if length == 0 {
		return nil, errors.New("pkcs7: data is empty")
	}

	unpadding := int(origData[length-1])
	if unpadding == 0 || unpadding > length {
		return nil, errors.New("pkcs7: invalid padding size")
	}

	for i := length - unpadding; i < length; i++ {
		if origData[i] != byte(unpadding) {
			return nil, errors.New("pkcs7: invalid padding bytes")
		}
	}

	return origData[:(length - unpadding)], nil
}
