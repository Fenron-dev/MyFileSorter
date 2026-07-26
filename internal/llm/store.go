package llm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dennis/myfilesorter/internal/domain"
)

type profileRecord struct {
	Profile       domain.AIProfile `json:"profile"`
	EncryptedKey string           `json:"encryptedKey,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	directory string
	filePath string
	keyPath  string
	records  map[string]profileRecord
	initErr  error
}

func NewStore(directory string) *Store {
	store := &Store{records: make(map[string]profileRecord)}
	if directory == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			store.initErr = fmt.Errorf("AI-Profilordner bestimmen: %w", err)
			return store
		}
		directory = filepath.Join(config, "MyFileSorter", "ai")
	}
	store.directory = directory
	store.filePath = filepath.Join(directory, "profiles.json")
	store.keyPath = filepath.Join(directory, "vault.key")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		store.initErr = fmt.Errorf("AI-Profilordner anlegen: %w", err)
		return store
	}
	if err := store.load(); err != nil {
		store.initErr = err
	}
	return store
}

func (s *Store) List() ([]domain.AIProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.initErr != nil {
		return nil, s.initErr
	}
	result := make([]domain.AIProfile, 0, len(s.records))
	for _, record := range s.records {
		profile := record.Profile
		profile.HasAPIKey = record.EncryptedKey != ""
		result = append(result, profile)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDefault != result[j].IsDefault {
			return result[i].IsDefault
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func (s *Store) Save(input domain.AIProfileInput) (domain.AIProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return domain.AIProfile{}, s.initErr
	}
	profile, err := normaliseProfile(input)
	if err != nil {
		return domain.AIProfile{}, err
	}
	if profile.ID == "" {
		profile.ID, err = randomID()
		if err != nil {
			return domain.AIProfile{}, err
		}
	}
	record := s.records[profile.ID]
	record.Profile = profile
	if input.ClearAPIKey {
		record.EncryptedKey = ""
	} else if strings.TrimSpace(input.APIKey) != "" {
		record.EncryptedKey, err = s.encrypt(strings.TrimSpace(input.APIKey))
		if err != nil {
			return domain.AIProfile{}, err
		}
	}
	if profile.IsDefault {
		for id, existing := range s.records {
			existing.Profile.IsDefault = false
			s.records[id] = existing
		}
	}
	s.records[profile.ID] = record
	if err := s.persist(); err != nil {
		return domain.AIProfile{}, err
	}
	profile.HasAPIKey = record.EncryptedKey != ""
	return profile, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return s.initErr
	}
	if _, found := s.records[id]; !found {
		return fmt.Errorf("AI-Profil %q wurde nicht gefunden", id)
	}
	delete(s.records, id)
	return s.persist()
}

func (s *Store) profileWithSecret(id string) (domain.AIProfile, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.initErr != nil {
		return domain.AIProfile{}, "", s.initErr
	}
	record, found := s.records[id]
	if !found {
		return domain.AIProfile{}, "", fmt.Errorf("AI-Profil %q wurde nicht gefunden", id)
	}
	secret := ""
	var err error
	if record.EncryptedKey != "" {
		secret, err = s.decrypt(record.EncryptedKey)
	}
	return record.Profile, secret, err
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("AI-Profile lesen: %w", err)
	}
	var records []profileRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("AI-Profile sind beschädigt: %w", err)
	}
	for _, record := range records {
		if record.Profile.ID != "" {
			s.records[record.Profile.ID] = record
		}
	}
	return nil
}

func (s *Store) persist() error {
	records := make([]profileRecord, 0, len(s.records))
	for _, record := range s.records {
		record.Profile.HasAPIKey = false
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Profile.ID < records[j].Profile.ID })
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.filePath, data, 0o600); err != nil {
		return fmt.Errorf("AI-Profile schreiben: %w", err)
	}
	return nil
}

func normaliseProfile(input domain.AIProfileInput) (domain.AIProfile, error) {
	profile := domain.AIProfile{
		ID: strings.TrimSpace(input.ID), Name: strings.TrimSpace(input.Name), Provider: strings.ToLower(strings.TrimSpace(input.Provider)),
		BaseURL: strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), Model: strings.TrimSpace(input.Model), IsDefault: input.IsDefault,
	}
	if profile.Name == "" || profile.Model == "" {
		return domain.AIProfile{}, fmt.Errorf("Profilname und Modell sind erforderlich")
	}
	defaults := map[string]string{
		"ollama": "http://127.0.0.1:11434", "lmstudio": "http://127.0.0.1:1234/v1",
		"openai": "https://api.openai.com/v1", "openrouter": "https://openrouter.ai/api/v1",
		"groq": "https://api.groq.com/openai/v1", "openai_compatible": "",
	}
	defaultURL, found := defaults[profile.Provider]
	if !found {
		return domain.AIProfile{}, fmt.Errorf("nicht unterstützter AI-Anbieter %q", profile.Provider)
	}
	if profile.BaseURL == "" {
		profile.BaseURL = defaultURL
	}
	if err := validateBaseURL(profile.BaseURL); err != nil {
		return domain.AIProfile{}, err
	}
	return profile, nil
}

func (s *Store) encrypt(value string) (string, error) {
	key, err := s.vaultKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(value), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (s *Store) decrypt(value string) (string, error) {
	key, err := s.vaultKey()
	if err != nil {
		return "", err
	}
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("AI-Schlüssel dekodieren: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("verschlüsselter AI-Schlüssel ist beschädigt")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("AI-Schlüssel entschlüsseln: %w", err)
	}
	return string(plain), nil
}

func (s *Store) vaultKey() ([]byte, error) {
	key, err := os.ReadFile(s.keyPath)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("AI-Tresorschlüssel ist beschädigt")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("AI-Tresorschlüssel lesen: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.keyPath, key, 0o600); err != nil {
		return nil, fmt.Errorf("AI-Tresorschlüssel schreiben: %w", err)
	}
	return key, nil
}

func randomID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
