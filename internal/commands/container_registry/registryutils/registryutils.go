package registryutils

import (
	"fmt"
	"strconv"
	"time"

	"github.com/dustin/go-humanize"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/tableprinter"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

func ParseID(value string, name string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}

	return id, nil
}

func DisplayRepositories(io *iostreams.IOStreams, repositories []*gitlab.RegistryRepository, showTagsCount bool) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	if showTagsCount {
		table.AddRow("ID", "Name", "Path", "Tags", "Created")
	} else {
		table.AddRow("ID", "Name", "Path", "Created")
	}

	for _, repository := range repositories {
		if showTagsCount {
			table.AddRow(
				repository.ID,
				repository.Name,
				repository.Path,
				repository.TagsCount,
				c.Gray(timeAgo(repository.CreatedAt)),
			)
		} else {
			table.AddRow(
				repository.ID,
				repository.Name,
				repository.Path,
				c.Gray(timeAgo(repository.CreatedAt)),
			)
		}
	}

	return table.Render()
}

func DisplayRepository(io *iostreams.IOStreams, repository *gitlab.RegistryRepository) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow("ID", repository.ID)
	table.AddRow("Name", repository.Name)
	table.AddRow("Path", repository.Path)
	table.AddRow("Project ID", repository.ProjectID)
	table.AddRow("Location", repository.Location)
	table.AddRow("Tags", repository.TagsCount)
	table.AddRow("Status", statusString(repository.Status))
	table.AddRow("Created", timeAgo(repository.CreatedAt))
	table.AddRow("Cleanup policy started", timeAgo(repository.CleanupPolicyStartedAt))

	return fmt.Sprintf("%s\n%s", c.Bold(repository.Path), table.Render())
}

func DisplayTags(tags []*gitlab.RegistryRepositoryTag) string {
	table := tableprinter.NewTablePrinter()
	table.AddRow("Name", "Path", "Location")

	for _, tag := range tags {
		table.AddRow(
			tag.Name,
			tag.Path,
			tag.Location,
		)
	}

	return table.Render()
}

func DisplayTagsWithDetails(io *iostreams.IOStreams, tags []*gitlab.RegistryRepositoryTag) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow("Name", "Path", "Digest", "Size", "Created")

	for _, tag := range tags {
		table.AddRow(
			tag.Name,
			tag.Path,
			tag.Digest,
			humanize.Bytes(uint64(tag.TotalSize)),
			c.Gray(timeAgo(tag.CreatedAt)),
		)
	}

	return table.Render()
}

func DisplayTag(io *iostreams.IOStreams, tag *gitlab.RegistryRepositoryTag) string {
	c := io.Color()
	table := tableprinter.NewTablePrinter()
	table.AddRow("Name", tag.Name)
	table.AddRow("Path", tag.Path)
	table.AddRow("Location", tag.Location)
	table.AddRow("Revision", tag.Revision)
	table.AddRow("Short revision", tag.ShortRevision)
	table.AddRow("Digest", tag.Digest)
	table.AddRow("Size", humanize.Bytes(uint64(tag.TotalSize)))
	table.AddRow("Created", timeAgo(tag.CreatedAt))

	return fmt.Sprintf("%s\n%s", c.Bold(tag.Name), table.Render())
}

func ProjectScopedRepositoryError(action string, repositoryID int64, repoName string) string {
	return fmt.Sprintf("%s repository %d on %s; ensure the container registry repository belongs to %s, or specify the owning project with -R <project>", action, repositoryID, repoName, repoName)
}

func ProjectScopedTagError(action string, tagName string, repositoryID int64, repoName string) string {
	return fmt.Sprintf("%s tag %q from repository %d on %s; ensure the container registry repository belongs to %s, or specify the owning project with -R <project>", action, tagName, repositoryID, repoName, repoName)
}

func statusString(status *gitlab.ContainerRegistryStatus) string {
	if status == nil {
		return ""
	}

	return string(*status)
}

func timeAgo(t *time.Time) string {
	if t == nil {
		return ""
	}

	return utils.TimeToPrettyTimeAgo(*t)
}
