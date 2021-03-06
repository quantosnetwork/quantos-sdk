package sdk

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/quantosnetwork/dev-0.1.0/common"
	account2 "github.com/quantosnetwork/dev-0.1.0/core/account"
	"golang.org/x/crypto/scrypt"
	"lukechampine.com/frand"
	"os"
	"time"
)

type KeyPairInfo struct {
	ID           string `json:"kp_id"`
	RawKey       string `json:"raw_key,omitempty"`
	KeyType      string `json:"key_type"`
	PubKey       string `json:"public_key"`
	Salt         string `json:"salt,omitempty"`
	EncryptedKey string `json:"encrypted_key,omitempty"`
	Mac          string `json:"mac,omitempty"`
}

func NewKeyPairInfo(rawKey string, keyType string) (*KeyPairInfo, error) {
	if rawKey == "" {
		return nil, fmt.Errorf("empty key")
	}
	kp := &KeyPairInfo{}
	kp.RawKey = rawKey
	kp.KeyType = keyType
	id, _ := uuid.NewUUID()
	kp.ID = id.String()
	_, pub := account2.NewKeyPair(id.String())
	pubb, err := pub.MarshalBinary()
	if err != nil {
		return nil, err
	}
	kp.PubKey = common.EncodeBase58(pubb)
	return kp, nil
}

func (k *KeyPairInfo) ToKeyPair() (*account2.LoadedKeys, error) {
	if k.RawKey == "" {
		return nil, fmt.Errorf("empty keypair")
	}
	priv, pub := account2.NewKeyPair(k.ID)
	lk := &account2.LoadedKeys{
		Priv:       priv,
		Pub:        pub,
		PubKeySign: nil,
	}
	return lk, nil

}

func (k *KeyPairInfo) IsEncrypted() bool {
	return k.EncryptedKey != "" || k.RawKey == ""
}

func (k *KeyPairInfo) Encrypt(password []byte) error {
	if k.IsEncrypted() {
		return errors.New("already encrypted")
	}
	salt := make([]byte, 48)
	frand.Read(salt[0:32])
	key, err := scrypt.Key(password, salt[0:32], 32768, 8, 1, 32)
	if err != nil {
		return err
	}
	aesBlock, err := aes.NewCipher(key[0:16])
	if err != nil {
		return err
	}
	stream := cipher.NewCTR(aesBlock, salt[32:48])
	inText := common.DecodeBase58(k.RawKey)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	mac := common.Sha3(append(key[16:32], outText...))
	k.Salt = common.EncodeBase58(salt)
	k.Mac = common.EncodeBase58(mac)
	k.RawKey = ""
	return nil
}

func (k *KeyPairInfo) Decrypt(password []byte) error {
	if !k.IsEncrypted() {
		return fmt.Errorf("not encrypted")
	}
	salt := common.DecodeBase58(k.Salt)
	key, err := scrypt.Key(password, salt[0:32], 32768, 8, 1, 32)
	if err != nil {
		return err
	}
	aesBlock, err := aes.NewCipher(key[0:16])
	if err != nil {
		return err
	}
	stream := cipher.NewCTR(aesBlock, salt[32:48])
	inText := common.DecodeBase58(k.EncryptedKey)
	outText := make([]byte, len(inText))
	stream.XORKeyStream(outText, inText)
	mac := common.Sha3(append(key[16:32], inText...))
	if !bytes.Equal(mac, common.DecodeBase58(k.Mac)) {
		return fmt.Errorf("wrong password")
	}
	k.RawKey = common.EncodeBase58(outText)
	return nil

}

type AccountInfo struct {
	Name     string                  `json:"name"`
	Keypairs map[string]*KeyPairInfo `json:"keypairs"`
}

func NewAccountInfo() *AccountInfo {
	return &AccountInfo{Name: "", Keypairs: make(map[string]*KeyPairInfo)}
}

func (a *AccountInfo) GetKeyPair(perm string) (*account2.LoadedKeys, error) {
	kp, ok := a.Keypairs[perm]
	if !ok {
		return nil, fmt.Errorf("invalid permission %v", perm)
	}
	return kp.ToKeyPair()

}

func (a *AccountInfo) IsEncrypted() bool {
	for _, kp := range a.Keypairs {
		if kp.IsEncrypted() {
			return true
		}
	}
	return false
}

func (a *AccountInfo) Decrypt(password []byte) error {
	if !a.IsEncrypted() {
		return fmt.Errorf("not encrypted")
	}
	for _, kp := range a.Keypairs {
		err := kp.Decrypt(password)
		if err != nil {
			return err
		}
	}
	fmt.Println("decrypt keystore succeed")
	return nil
}

func (a *AccountInfo) Encrypt(password []byte) error {
	if a.IsEncrypted() {
		return fmt.Errorf("account already encrypted")
	}
	for _, k := range a.Keypairs {
		err := k.Encrypt(password)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *AccountInfo) SaveTo(fileName string) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("saving keyfile of account %v to %v\n", a.Name, fileName)
	err = os.WriteFile(fileName, data, 0400)
	return err
}

func LoadAccountFrom(fileName string) (*AccountInfo, error) {
	data, err := os.ReadFile(fileName)
	if err != nil {
		return nil, err
	}
	a := NewAccountInfo()
	err = json.Unmarshal(data, a)
	if err != nil {
		return nil, fmt.Errorf("key store should be a json file, %v", err)
	}
	return a, nil
}

type FileAccountStore struct {
	AccountDir string
}

func NewFileAccountStore(accountDir string) *FileAccountStore {
	return &FileAccountStore{accountDir}
}

func (s *FileAccountStore) LoadAccount(name string) (*AccountInfo, error) {
	fileName := s.AccountDir + "/" + name + ".json"
	_, err := os.Stat(fileName)
	if err != nil {
		return nil, fmt.Errorf("account is not imported at %s: %v. use 'iwallet account import %s <private-key>' to import it", fileName, err, name)
	}
	return LoadAccountFrom(fileName)
}

func (s *FileAccountStore) SaveAccount(a *AccountInfo) error {
	dir := s.AccountDir
	err := os.MkdirAll(s.AccountDir, 0700)
	if err != nil {
		return err
	}
	fileName := dir + "/" + a.Name + ".json"
	// back up old keystore file if needed
	if _, err := os.Stat(fileName); !os.IsNotExist(err) {
		timeStr := time.Now().Format(time.RFC3339)
		backupDir := dir + "/backup"
		err = os.MkdirAll(backupDir, 0700)
		if err != nil {
			return err
		}
		backupFileName := backupDir + "/" + a.Name + "." + timeStr + ".json"
		fmt.Printf("backing up %v to %v\n", fileName, backupFileName)
		err = os.Rename(fileName, backupFileName)
		if err != nil {
			return err
		}
	}
	return a.SaveTo(fileName)
}

func (s *FileAccountStore) DeleteAccount(name string) error {
	f := s.AccountDir + "/" + name + ".json"
	err := os.Remove(f)
	if err != nil {
		return err
	}
	fmt.Println("File", f, "has been removed.")
	return nil
}

func (s *FileAccountStore) ListAccounts() ([]*AccountInfo, error) {
	files, err := os.ReadDir(s.AccountDir)
	if err != nil {
		return nil, err
	}
	accs := make([]*AccountInfo, 0)
	for _, f := range files {
		fileName := s.AccountDir + "/" + f.Name()
		acc, err := LoadAccountFrom(fileName)
		if err != nil {
			fmt.Println("loading account failed", fileName, err)
			continue
		}
		accs = append(accs, acc)
	}
	return accs, nil
}
