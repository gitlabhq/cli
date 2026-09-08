package registryutils

import (
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

type RepositoryJSON struct {
	ID                     int64      `json:"id"`
	Name                   string     `json:"name"`
	Path                   string     `json:"path"`
	ProjectID              int64      `json:"project_id"`
	Location               string     `json:"location"`
	CreatedAt              *time.Time `json:"created_at"`
	CleanupPolicyStartedAt *time.Time `json:"cleanup_policy_started_at"`
	Status                 *string    `json:"status"`
	TagsCount              *int64     `json:"tags_count,omitempty"`
	Tags                   []TagJSON  `json:"tags,omitempty"`
}

type TagJSON struct {
	Name          string     `json:"name"`
	Path          string     `json:"path"`
	Location      string     `json:"location"`
	Revision      string     `json:"revision,omitempty"`
	ShortRevision string     `json:"short_revision,omitempty"`
	Digest        string     `json:"digest,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	TotalSize     *int64     `json:"total_size,omitempty"`
}

func NewRepositoryJSONList(repositories []*gitlab.RegistryRepository, includeTagDetails bool, showTagsCount bool) []RepositoryJSON {
	output := make([]RepositoryJSON, 0, len(repositories))
	for _, repository := range repositories {
		output = append(output, NewRepositoryJSON(repository, includeTagDetails, showTagsCount))
	}

	return output
}

func NewRepositoryJSON(repository *gitlab.RegistryRepository, includeTagDetails bool, showTagsCount bool) RepositoryJSON {
	var tagsCount *int64
	if showTagsCount {
		tagCount := repository.TagsCount
		tagsCount = &tagCount
	}

	return RepositoryJSON{
		ID:                     repository.ID,
		Name:                   repository.Name,
		Path:                   repository.Path,
		ProjectID:              repository.ProjectID,
		Location:               repository.Location,
		CreatedAt:              repository.CreatedAt,
		CleanupPolicyStartedAt: repository.CleanupPolicyStartedAt,
		Status:                 statusStringPointer(repository.Status),
		TagsCount:              tagsCount,
		Tags:                   NewTagJSONList(repository.Tags, includeTagDetails),
	}
}

func NewTagJSONList(tags []*gitlab.RegistryRepositoryTag, includeDetails bool) []TagJSON {
	if len(tags) == 0 {
		return nil
	}

	output := make([]TagJSON, 0, len(tags))
	for _, tag := range tags {
		output = append(output, NewTagJSON(tag, includeDetails))
	}

	return output
}

func NewTagJSON(tag *gitlab.RegistryRepositoryTag, includeDetails bool) TagJSON {
	tagOutput := TagJSON{
		Name:     tag.Name,
		Path:     tag.Path,
		Location: tag.Location,
	}
	if includeDetails {
		tagOutput.Revision = tag.Revision
		tagOutput.ShortRevision = tag.ShortRevision
		tagOutput.Digest = tag.Digest
		tagOutput.CreatedAt = tag.CreatedAt
		tagOutput.TotalSize = new(tag.TotalSize)
	}

	return tagOutput
}

func statusStringPointer(status *gitlab.ContainerRegistryStatus) *string {
	if status == nil {
		return nil
	}

	statusValue := string(*status)
	return &statusValue
}
