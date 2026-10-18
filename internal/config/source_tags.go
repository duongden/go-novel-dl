package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm/clause"
)

const (
	maxSourceTagCount        = 16
	maxSourceTagRuneSize     = 24
	sourceTagDefaultsVersion = 2
)

type SourceTagRecord struct {
	SiteKey   string    `json:"site_key"`
	Tags      []string  `json:"tags"`
	UpdatedAt time.Time `json:"updated_at"`
}

type sourceTagEntry struct {
	SiteKey        string    `gorm:"primaryKey;size:64"`
	Tags           string    `gorm:"type:text;not null"`
	IsCustom       bool      `gorm:"not null;default:false"`
	DefaultVersion int       `gorm:"not null;default:0"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

func (sourceTagEntry) TableName() string {
	return "source_tags"
}

// EnsureSourceTags upgrades bundled tags while preserving user changes.
//
// previousDefaults is only needed for databases created before source tags
// tracked whether a row had been customized.
func EnsureSourceTags(defaults, previousDefaults map[string][]string) (map[string][]string, error) {
	if err := ensureSiteCatalogDB(); err != nil {
		return nil, err
	}

	type normalizedDefault struct {
		payload string
	}

	bundled := make(map[string]normalizedDefault, len(defaults))
	for siteKey, tags := range defaults {
		siteKey = strings.ToLower(strings.TrimSpace(siteKey))
		if siteKey == "" {
			continue
		}
		normalized := normalizeSourceTags(tags)
		if err := validateSourceTags(normalized); err != nil {
			return nil, fmt.Errorf("invalid defaults for %s: %w", siteKey, err)
		}
		payload, err := json.Marshal(normalized)
		if err != nil {
			return nil, err
		}
		bundled[siteKey] = normalizedDefault{payload: string(payload)}
	}

	previous := make(map[string][]string, len(previousDefaults))
	for siteKey, tags := range previousDefaults {
		siteKey = strings.ToLower(strings.TrimSpace(siteKey))
		if siteKey == "" {
			continue
		}
		previous[siteKey] = normalizeSourceTags(tags)
	}

	var existing []sourceTagEntry
	if err := siteCatalogDB.Find(&existing).Error; err != nil {
		return nil, err
	}
	existingByKey := make(map[string]sourceTagEntry, len(existing))
	for _, entry := range existing {
		existingByKey[entry.SiteKey] = entry
	}

	records := make([]sourceTagEntry, 0, len(bundled))
	for siteKey, item := range bundled {
		entry, ok := existingByKey[siteKey]
		if !ok {
			records = append(records, sourceTagEntry{
				SiteKey:        siteKey,
				Tags:           item.payload,
				DefaultVersion: sourceTagDefaultsVersion,
			})
			continue
		}
		if entry.IsCustom {
			continue
		}

		if entry.DefaultVersion == 0 {
			legacyTags, knowsLegacyTags := previous[siteKey]
			if !knowsLegacyTags || !slices.Equal(parseSourceTags(entry.Tags), legacyTags) {
				if err := siteCatalogDB.Model(&sourceTagEntry{}).
					Where("site_key = ?", siteKey).
					Updates(map[string]any{
						"is_custom":       true,
						"default_version": sourceTagDefaultsVersion,
						"updated_at":      time.Now(),
					}).Error; err != nil {
					return nil, err
				}
				continue
			}
		}

		if entry.Tags == item.payload && entry.DefaultVersion == sourceTagDefaultsVersion {
			continue
		}
		if err := siteCatalogDB.Model(&sourceTagEntry{}).
			Where("site_key = ?", siteKey).
			Updates(map[string]any{
				"tags":            item.payload,
				"is_custom":       false,
				"default_version": sourceTagDefaultsVersion,
				"updated_at":      time.Now(),
			}).Error; err != nil {
			return nil, err
		}
	}
	if len(records) > 0 {
		if err := siteCatalogDB.Create(&records).Error; err != nil {
			return nil, err
		}
	}

	items, err := ListSourceTags()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(items))
	for _, item := range items {
		result[item.SiteKey] = item.Tags
	}
	return result, nil
}

func ListSourceTags() ([]SourceTagRecord, error) {
	if err := ensureSiteCatalogDB(); err != nil {
		return nil, err
	}
	var entries []sourceTagEntry
	if err := siteCatalogDB.Order("site_key ASC").Find(&entries).Error; err != nil {
		return nil, err
	}
	items := make([]SourceTagRecord, 0, len(entries))
	for _, entry := range entries {
		items = append(items, sourceTagRecord(entry))
	}
	return items, nil
}

func UpdateSourceTags(siteKey string, tags []string) (SourceTagRecord, error) {
	siteKey = strings.ToLower(strings.TrimSpace(siteKey))
	if siteKey == "" {
		return SourceTagRecord{}, fmt.Errorf("site key is required")
	}
	normalized := normalizeSourceTags(tags)
	if err := validateSourceTags(normalized); err != nil {
		return SourceTagRecord{}, err
	}
	if err := ensureSiteCatalogDB(); err != nil {
		return SourceTagRecord{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return SourceTagRecord{}, err
	}
	entry := sourceTagEntry{
		SiteKey:        siteKey,
		Tags:           string(payload),
		IsCustom:       true,
		DefaultVersion: sourceTagDefaultsVersion,
	}
	if err := siteCatalogDB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "site_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"tags", "is_custom", "default_version", "updated_at"}),
	}).Create(&entry).Error; err != nil {
		return SourceTagRecord{}, err
	}
	var saved sourceTagEntry
	if err := siteCatalogDB.Where("site_key = ?", siteKey).Take(&saved).Error; err != nil {
		return SourceTagRecord{}, err
	}
	return sourceTagRecord(saved), nil
}

func sourceTagRecord(entry sourceTagEntry) SourceTagRecord {
	return SourceTagRecord{
		SiteKey:   entry.SiteKey,
		Tags:      parseSourceTags(entry.Tags),
		UpdatedAt: entry.UpdatedAt,
	}
}

func parseSourceTags(raw string) []string {
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{}
	}
	normalized := normalizeSourceTags(tags)
	if normalized == nil {
		return []string{}
	}
	return normalized
}

func normalizeSourceTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, raw := range tags {
		tag := strings.TrimSpace(strings.ToValidUTF8(raw, ""))
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized
}

func validateSourceTags(tags []string) error {
	if len(tags) > maxSourceTagCount {
		return fmt.Errorf("每个渠道最多设置 %d 个标签", maxSourceTagCount)
	}
	for _, tag := range tags {
		if utf8.RuneCountInString(tag) > maxSourceTagRuneSize {
			return fmt.Errorf("标签“%s”超过 %d 个字符", tag, maxSourceTagRuneSize)
		}
	}
	return nil
}
