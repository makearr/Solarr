package spec

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
)

// Fetch gets the file a result links to. A link that is only a details page is opened and its download link found by the
// spec's selectors; a magnet link found there is the release itself, and comes back as its own bytes.
func (d *Driver) Fetch(ctx context.Context, link string) ([]byte, error) {
	download := d.spec.Download
	target := link
	if download != nil && (len(download.Selectors) > 0 || download.Infohash != nil || download.Before != nil) {
		resolved, err := d.resolveDownload(ctx, link, download)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(resolved, "magnet:") {
			return []byte(resolved), nil
		}
		target = resolved
	}
	method := http.MethodGet
	if download != nil && strings.EqualFold(download.Method, "post") {
		method = http.MethodPost
	}
	var headers map[string][]string
	if download != nil {
		headers = download.Headers
	}
	answer, err := d.fetch(ctx, method, target, url.Values{}, headers)
	if err != nil {
		return nil, err
	}
	if answerIsHTML(answer.body) {
		return nil, fmt.Errorf("spec: %s answered a web page, not a file: the sign-in may have ended", d.spec.ID)
	}
	return answer.body, nil
}

// resolveDownload opens the details page and reads the file's address, or a torrent's hash, from it.
func (d *Driver) resolveDownload(ctx context.Context, link string, download *Download) (string, error) {
	variables := d.variables()
	variables.DownloadURI = map[string]string{"AbsoluteUri": link}
	if parsed, err := url.Parse(link); err == nil {
		variables.DownloadURI["AbsolutePath"] = parsed.Path
		variables.DownloadURI["Query"] = parsed.RawQuery
	}
	opened := link
	if download.Before != nil && download.Before.Path != "" {
		path, err := render(download.Before.Path, variables)
		if err != nil {
			return "", err
		}
		if opened, err = d.resolve(path); err != nil {
			return "", err
		}
	}
	answer, err := d.fetch(ctx, http.MethodGet, opened, nil, download.Headers)
	if err != nil {
		return "", err
	}
	found, err := answer.document()
	if err != nil {
		return "", err
	}
	context := filterContext{variables: variables, now: d.clock.Now()}
	source := htmlNode{found.Selection}
	for _, selector := range download.Selectors {
		value, ok, err := readField(source, withName(selector, "download"), context)
		if err != nil || !ok || value == "" {
			continue
		}
		if strings.HasPrefix(value, "magnet:") {
			return value, nil
		}
		return d.absolute(value), nil
	}
	if download.Infohash != nil && download.Infohash.Hash != nil {
		hash, ok, err := readField(source, withName(*download.Infohash.Hash, "infohash"), context)
		if err == nil && ok && hash != "" {
			title := ""
			if download.Infohash.Title != nil {
				title, _, _ = readField(source, withName(*download.Infohash.Title, "title"), context)
			}
			return indexer.Magnet(hash, title, nil), nil
		}
	}
	return "", fmt.Errorf("spec: %s: no download link found on the details page", d.spec.ID)
}

func withName(extractor Extractor, name string) Extractor {
	extractor.Name = name
	return extractor
}
