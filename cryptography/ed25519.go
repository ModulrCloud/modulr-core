package cryptography

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"

	"github.com/btcsuite/btcutil/base58"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
)

type Ed25519Box struct {
	Mnemonic  string
	Bip44Path []uint32
	Pub, Prv  string
}

func GenerateKeyPair(mnemonic, mnemonicPassword string, bip44DerivePath []uint32) Ed25519Box {
	if mnemonic == "" {
		// Generate mnemonic if no pre-set
		entropy, _ := bip39.NewEntropy(256)
		mnemonic, _ = bip39.NewMnemonic(entropy)
	}

	// Now generate seed from 24-word mnemonic phrase (24 words = 256 bit security)
	// Seed has 64 bytes
	seed := bip39.NewSeed(mnemonic, mnemonicPassword) // password might be ""(empty) but it's not recommended

	// Generate master keypair from seed
	masterPrivateKey, _ := bip32.NewMasterKey(seed)

	// Now, to derive appropriate keypair - run the cycle over uint32 path-milestones and derive child keypairs
	// In case bip44Path empty - set the default one
	if len(bip44DerivePath) == 0 {
		bip44DerivePath = []uint32{44, 7337, 0, 0}
	}

	// Start derivation from master private key
	var childKey *bip32.Key = masterPrivateKey
	for _, pathPart := range bip44DerivePath {
		childKey, _ = childKey.NewChildKey(bip32.FirstHardenedChild + pathPart)
	}

	// Now, based on this - get the appropriate keypair
	publicKeyObject, privateKeyObject := generateKeyPairFromSeed(childKey.Key)

	// Export keypair
	pubKeyBytes, _ := x509.MarshalPKIXPublicKey(publicKeyObject)
	privKeyBytes, _ := x509.MarshalPKCS8PrivateKey(privateKeyObject)

	return Ed25519Box{Mnemonic: mnemonic, Bip44Path: bip44DerivePath, Pub: base58.Encode(pubKeyBytes[12:]), Prv: base64.StdEncoding.EncodeToString(privKeyBytes)}
}

func GenerateSignature(base64PrivateKey, msg string) string {
	// Decode private key from base64 to raw bytes
	privateKeyAsBytes, _ := base64.StdEncoding.DecodeString(base64PrivateKey)

	// Deserialize private key
	privKeyInterface, _ := x509.ParsePKCS8PrivateKey(privateKeyAsBytes)
	finalPrivateKey := privKeyInterface.(ed25519.PrivateKey)

	msgAsBytes := []byte(msg)
	signature, _ := finalPrivateKey.Sign(rand.Reader, msgAsBytes, crypto.Hash(0))

	return base64.StdEncoding.EncodeToString(signature)
}

func VerifySignature(message, base58PubKey, base64Signature string) bool {
	msgAsBytes := []byte(message)

	// Pubkey must be base58-decoded to raw 32 bytes (ed25519).
	publicKeyAsBytesWithNoAsnPrefix := base58.Decode(base58PubKey)
	if len(publicKeyAsBytesWithNoAsnPrefix) != ed25519.PublicKeySize {
		return false
	}

	// Add ASN.1 prefix for x509.ParsePKIXPublicKey.
	pubKeyAsBytesWithAsnPrefix := append(
		[]byte{0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00},
		publicKeyAsBytesWithNoAsnPrefix...,
	)

	pubKeyInterface, err := x509.ParsePKIXPublicKey(pubKeyAsBytesWithAsnPrefix)
	if err != nil {
		return false
	}
	finalPubKey, ok := pubKeyInterface.(ed25519.PublicKey)
	if !ok {
		return false
	}

	signature, err := base64.StdEncoding.DecodeString(base64Signature)
	if err != nil {
		return false
	}

	return ed25519.Verify(finalPubKey, msgAsBytes, signature)
}

// IsValidPubKey validates that a string is a canonical Modulr public key:
// base58-encoded raw ed25519 public key bytes (32 bytes).
func IsValidPubKey(base58PubKey string) bool {
	return len(base58.Decode(base58PubKey)) == ed25519.PublicKeySize
}

// Private inner function
func generateKeyPairFromSeed(seed []byte) (ed25519.PublicKey, ed25519.PrivateKey) {

	privateKey := ed25519.NewKeyFromSeed(seed)

	pubKey, _ := privateKey.Public().(ed25519.PublicKey)

	return pubKey, privateKey

}
