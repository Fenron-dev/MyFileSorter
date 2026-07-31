package llm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dennis/myfilesorter/internal/domain"
	"github.com/zalando/go-keyring"
)

const keyringService = "MyFileSorter"

const (
	maxProfileFileBytes = 4 << 20
	maxProfiles         = 100
)

type profileRecord struct {
	Profile      domain.AIProfile `json:"profile"`
	SecretStored bool             `json:"secretStored,omitempty"`
	// EncryptedKey is retained only to migrate profiles created by versions
	// that kept an application key next to the encrypted profile file.
	EncryptedKey string `json:"encryptedKey,omitempty"`
}

type Store struct {
	mu        sync.RWMutex
	directory string
	filePath  string
	keyPath   string
	records   map[string]profileRecord
	initErr   error
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
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		store.initErr = fmt.Errorf("AI-Profilordner ist kein sicheres Verzeichnis")
		return store
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		store.initErr = fmt.Errorf("AI-Profilordner absichern: %w", err)
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
		profile.HasAPIKey = record.SecretStored || record.EncryptedKey != ""
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
	record, existed := s.records[profile.ID]
	if !existed && len(s.records) >= maxProfiles {
		return domain.AIProfile{}, fmt.Errorf("maximal %d AI-Profile sind erlaubt", maxProfiles)
	}
	hasSecret := record.SecretStored || record.EncryptedKey != ""
	if input.ClearAPIKey {
		hasSecret = false
	} else if strings.TrimSpace(input.APIKey) != "" {
		hasSecret = true
	}
	if len(input.APIKey) > 16*1024 {
		return domain.AIProfile{}, fmt.Errorf("API-Schlüssel ist zu lang")
	}
	if err := validateEndpointSecurity(profile, hasSecret); err != nil {
		return domain.AIProfile{}, err
	}
	previousRecords := cloneProfileRecords(s.records)
	previousSecret, secretErr := s.currentKeyringSecret(profile.ID, record)
	if secretErr != nil && (input.ClearAPIKey || strings.TrimSpace(input.APIKey) != "") {
		return domain.AIProfile{}, secretErr
	}
	keyringChanged := false
	record.Profile = profile
	if input.ClearAPIKey {
		if err := deleteKeyringSecret(profile.ID); err != nil {
			return domain.AIProfile{}, err
		}
		keyringChanged = record.SecretStored
		record.SecretStored = false
		record.EncryptedKey = ""
	} else if strings.TrimSpace(input.APIKey) != "" {
		if err := keyring.Set(keyringService, keyringAccount(profile.ID), strings.TrimSpace(input.APIKey)); err != nil {
			return domain.AIProfile{}, fmt.Errorf("API-Schlüssel im System-Schlüsselbund speichern: %w", err)
		}
		keyringChanged = true
		record.SecretStored = true
		record.EncryptedKey = ""
	}
	if profile.IsDefault {
		for id, existing := range s.records {
			existing.Profile.IsDefault = false
			s.records[id] = existing
		}
	}
	s.records[profile.ID] = record
	if err := s.persist(); err != nil {
		s.records = previousRecords
		if keyringChanged {
			s.restoreKeyringSecret(profile.ID, previousSecret)
		}
		return domain.AIProfile{}, err
	}
	profile.HasAPIKey = record.SecretStored || record.EncryptedKey != ""
	return profile, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return s.initErr
	}
	record, found := s.records[id]
	if !found {
		return fmt.Errorf("AI-Profil %q wurde nicht gefunden", id)
	}
	previousSecret, err := s.currentKeyringSecret(id, record)
	if err != nil {
		return err
	}
	if record.SecretStored {
		if err := deleteKeyringSecret(id); err != nil {
			return err
		}
	}
	delete(s.records, id)
	if err := s.persist(); err != nil {
		s.records[id] = record
		s.restoreKeyringSecret(id, previousSecret)
		return err
	}
	return nil
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
	if record.SecretStored {
		secret, err := keyring.Get(keyringService, keyringAccount(id))
		if err != nil {
			return domain.AIProfile{}, "", fmt.Errorf("API-Schlüssel aus System-Schlüsselbund lesen: %w", err)
		}
		return record.Profile, secret, nil
	}
	if record.EncryptedKey != "" {
		secret, err := s.decrypt(record.EncryptedKey)
		return record.Profile, secret, err
	}
	return record.Profile, "", nil
}

func (s *Store) load() error {
	data, err := readRegularFile(s.filePath, maxProfileFileBytes)
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
	if len(records) > maxProfiles {
		return fmt.Errorf("AI-Profildatei enthält zu viele Profile")
	}
	migrated := false
	migrationDeferred := false
	legacyFallback := make(map[string]profileRecord)
	for _, record := range records {
		if record.Profile.ID != "" {
			if _, duplicate := s.records[record.Profile.ID]; duplicate {
				return fmt.Errorf("AI-Profildatei enthält doppelte Profil-IDs")
			}
			normalised, normaliseErr := normaliseProfile(domain.AIProfileInput{
				ID: record.Profile.ID, Name: record.Profile.Name, Provider: record.Profile.Provider,
				BaseURL: record.Profile.BaseURL, Model: record.Profile.Model, IsDefault: record.Profile.IsDefault,
			})
			if normaliseErr != nil {
				return fmt.Errorf("gespeichertes AI-Profil %q ist ungültig: %w", record.Profile.ID, normaliseErr)
			}
			record.Profile = normalised
			if securityErr := validateEndpointSecurity(record.Profile, record.SecretStored || record.EncryptedKey != ""); securityErr != nil {
				return fmt.Errorf("gespeichertes AI-Profil %q ist unsicher: %w", record.Profile.ID, securityErr)
			}
			if record.EncryptedKey != "" {
				secret, decryptErr := s.decrypt(record.EncryptedKey)
				if decryptErr != nil {
					return fmt.Errorf("alten AI-Schlüssel migrieren: %w", decryptErr)
				}
				// A Linux desktop can legitimately have no active Secret Service
				// session (for example over SSH). Keep the legacy ciphertext usable
				// in that case instead of making every profile unavailable.
				if setErr := keyring.Set(keyringService, keyringAccount(record.Profile.ID), secret); setErr == nil {
					legacyFallback[record.Profile.ID] = record
					record.SecretStored = true
					record.EncryptedKey = ""
					migrated = true
				} else {
					migrationDeferred = true
				}
			}
			s.records[record.Profile.ID] = record
		}
	}
	if migrationDeferred {
		for id, record := range legacyFallback {
			s.records[id] = record
		}
		migrated = false
	}
	if migrated {
		if err := s.persist(); err != nil {
			// The old file is still intact. Restore its in-memory representation
			// and let the next start retry migration safely.
			for id, record := range legacyFallback {
				s.records[id] = record
			}
			return nil
		}
		_ = os.Remove(s.keyPath)
		syncStoreDirectory(s.directory)
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
	if len(data) > maxProfileFileBytes {
		return fmt.Errorf("AI-Profildatei ist zu groß")
	}
	temporary, err := os.CreateTemp(s.directory, ".profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("AI-Profile vorbereiten: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("AI-Profile schreiben: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("AI-Profile synchronisieren: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceStoreFile(temporaryPath, s.filePath); err != nil {
		return fmt.Errorf("AI-Profile finalisieren: %w", err)
	}
	syncStoreDirectory(s.directory)
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
	if len([]rune(profile.ID)) > 128 || len([]rune(profile.Name)) > 120 || len([]rune(profile.Model)) > 240 || len(profile.BaseURL) > 2048 {
		return domain.AIProfile{}, fmt.Errorf("AI-Profildaten überschreiten die erlaubte Länge")
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
	key, err := readRegularFile(s.keyPath, 32)
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
	file, err := os.OpenFile(s.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return s.vaultKey()
	}
	if err != nil {
		return nil, fmt.Errorf("AI-Tresorschlüssel anlegen: %w", err)
	}
	if _, err := file.Write(key); err != nil {
		file.Close()
		return nil, fmt.Errorf("AI-Tresorschlüssel schreiben: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("AI-Tresorschlüssel synchronisieren: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	syncStoreDirectory(s.directory)
	return key, nil
}

func readRegularFile(path string, maximum int64) ([]byte, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Size() > maximum {
		return nil, fmt.Errorf("Datei ist ungültig oder zu groß")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		return nil, fmt.Errorf("Datei wurde beim Öffnen ausgetauscht")
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, fmt.Errorf("Datei ist zu groß oder nicht lesbar")
	}
	postInfo, err := file.Stat()
	if err != nil || !os.SameFile(openedInfo, postInfo) || int64(len(data)) != openedInfo.Size() {
		return nil, fmt.Errorf("Datei wurde während des Lesens verändert")
	}
	currentInfo, err := os.Lstat(path)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
		return nil, fmt.Errorf("Datei wurde nach dem Lesen ausgetauscht")
	}
	return data, nil
}

func randomID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func keyringAccount(profileID string) string {
	return "ai-profile:" + profileID
}

func deleteKeyringSecret(profileID string) error {
	err := keyring.Delete(keyringService, keyringAccount(profileID))
	if err == nil || err == keyring.ErrNotFound {
		return nil
	}
	return fmt.Errorf("API-Schlüssel aus System-Schlüsselbund löschen: %w", err)
}

func cloneProfileRecords(input map[string]profileRecord) map[string]profileRecord {
	result := make(map[string]profileRecord, len(input))
	for id, record := range input {
		result[id] = record
	}
	return result
}

func (s *Store) currentKeyringSecret(profileID string, record profileRecord) (string, error) {
	if !record.SecretStored {
		return "", nil
	}
	secret, err := keyring.Get(keyringService, keyringAccount(profileID))
	if err == keyring.ErrNotFound {
		// The user must still be able to repair or delete a profile after an
		// operating-system keychain cleanup removed its credential.
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("vorhandenen API-Schlüssel aus System-Schlüsselbund lesen: %w", err)
	}
	return secret, nil
}

func (s *Store) restoreKeyringSecret(profileID, secret string) {
	if secret == "" {
		_ = deleteKeyringSecret(profileID)
		return
	}
	_ = keyring.Set(keyringService, keyringAccount(profileID), secret)
}

func syncStoreDirectory(directory string) {
	handle, err := os.Open(directory)
	if err != nil {
		return
	}
	_ = handle.Sync()
	_ = handle.Close()
}
