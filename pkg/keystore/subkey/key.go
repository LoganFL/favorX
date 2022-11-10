package subkey

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/FavorLabs/favorX/pkg/crypto"
	"github.com/google/uuid"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
)

const (
	keyHeaderKDF = "scrypt"
	keyVersion   = 3

	scryptN     = 1 << 15
	scryptR     = 8
	scryptP     = 1
	scryptDKLen = 32
)

// This format is compatible with Ethereum JSON v3 key file format.
type encryptedKey struct {
	Address string    `json:"address"`
	Crypto  keyCripto `json:"crypto"`
	Version int       `json:"version"`
	Id      string    `json:"id"`
}

type keyCripto struct {
	Cipher       string       `json:"cipher"`
	CipherText   string       `json:"ciphertext"`
	CipherParams cipherParams `json:"cipherparams"`
	KDF          string       `json:"kdf"`
	KDFParams    kdfParams    `json:"kdfparams"`
	MAC          string       `json:"mac"`
}

type cipherParams struct {
	IV string `json:"iv"`
}

type kdfParams struct {
	N     int    `json:"n"`
	R     int    `json:"r"`
	P     int    `json:"p"`
	DKLen int    `json:"dklen"`
	Salt  string `json:"salt"`
}

func encryptKey(k crypto.Signer, password string) ([]byte, error) {
	var data []byte
	if k.GetMnemonic() != "" {
		data = []byte(k.GetMnemonic())
	} else {
		data = k.GetSeed()
	}
	kc, err := encryptData(data, []byte(password))
	if err != nil {
		return nil, err
	}
	return json.Marshal(encryptedKey{
		Address: fmt.Sprintf("%s", k.Public().Address()),
		Crypto:  *kc,
		Version: keyVersion,
		Id:      uuid.NewString(),
	})
}

func decryptKey(data []byte, password string) (crypto.Signer, error) {
	var k encryptedKey
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, err
	}
	if k.Version != keyVersion {
		return nil, fmt.Errorf("unsupported key version: %v", k.Version)
	}
	d, err := decryptData(k.Crypto, password)
	if err != nil {
		return nil, err
	}
	var keypair crypto.Signer
	if strings.Contains(string(d), " ") {
		keypair, err = crypto.NewKeypairFromMnemonic(string(d))
	} else {
		keypair, err = crypto.NewKeypairFromSeedHex(string(d))
	}
	if err != nil {
		return nil, err
	}
	return keypair, err
}

func encryptData(data, password []byte) (*keyCripto, error) {
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("read random data: %w", err)
	}
	derivedKey, err := scrypt.Key(password, salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return nil, err
	}
	encryptKey := derivedKey[:16]

	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("read random data: %w", err)
	}
	cipherText, err := aesCTRXOR(encryptKey, data, iv)
	if err != nil {
		return nil, err
	}
	hasher := sha3.NewLegacyKeccak256()
	_, err = hasher.Write(append(derivedKey[16:32], cipherText...))
	if err != nil {
		return nil, err
	}
	mac := hasher.Sum(nil)

	return &keyCripto{
		Cipher:     "aes-128-ctr",
		CipherText: hex.EncodeToString(cipherText),
		CipherParams: cipherParams{
			IV: hex.EncodeToString(iv),
		},
		KDF: keyHeaderKDF,
		KDFParams: kdfParams{
			N:     scryptN,
			R:     scryptR,
			P:     scryptP,
			DKLen: scryptDKLen,
			Salt:  hex.EncodeToString(salt),
		},
		MAC: hex.EncodeToString(mac[:]),
	}, nil
}

func decryptData(v keyCripto, password string) ([]byte, error) {
	if v.Cipher != "aes-128-ctr" {
		return nil, fmt.Errorf("unsupported cipher: %v", v.Cipher)
	}

	mac, err := hex.DecodeString(v.MAC)
	if err != nil {
		return nil, fmt.Errorf("hex decode mac: %s", err)
	}
	cipherText, err := hex.DecodeString(v.CipherText)
	if err != nil {
		return nil, fmt.Errorf("hex decode cipher text: %s", err)
	}
	derivedKey, err := getKDFKey(v, []byte(password))
	if err != nil {
		return nil, err
	}
	calculatedMAC := sha3.Sum256(append(derivedKey[16:32], cipherText...))
	if !bytes.Equal(calculatedMAC[:], mac) {
		// if this fails we might be trying to load an ethereum V3 keyfile
		hasher := sha3.NewLegacyKeccak256()
		_, err = hasher.Write(append(derivedKey[16:32], cipherText...))
		if err != nil {
			return nil, err
		}
		calculatedMACEth := hasher.Sum(nil)
		if !bytes.Equal(calculatedMACEth[:], mac) {
			return nil, errors.New("ErrInvalidPassword")
		}
	}

	iv, err := hex.DecodeString(v.CipherParams.IV)
	if err != nil {
		return nil, fmt.Errorf("hex decode IV cipher parameter: %s", err)
	}
	data, err := aesCTRXOR(derivedKey[:16], cipherText, iv)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func aesCTRXOR(key, inText, iv []byte) ([]byte, error) {
	aesBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	stream := cipher.NewCTR(aesBlock, iv)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	return outText, nil
}

func getKDFKey(v keyCripto, password []byte) ([]byte, error) {
	if v.KDF != keyHeaderKDF {
		return nil, fmt.Errorf("unsupported KDF: %s", v.KDF)
	}
	salt, err := hex.DecodeString(v.KDFParams.Salt)
	if err != nil {
		return nil, fmt.Errorf("hex decode salt: %s", err)
	}
	return scrypt.Key(
		password,
		salt,
		v.KDFParams.N,
		v.KDFParams.R,
		v.KDFParams.P,
		v.KDFParams.DKLen,
	)
}
