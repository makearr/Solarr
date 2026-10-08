package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrOrganisationNotFound reports an organisation no library the viewer may read knows of.
var ErrOrganisationNotFound = errors.New("library: organisation not found")

// Organisations are the labels, networks, studios and publishers of every media type (ADR-32).
type Organisations struct{ store *store.Store }

// NewOrganisations wraps an opened library database.
func NewOrganisations(opened *store.Store) Organisations { return Organisations{store: opened} }

// OrganisationFilter is PeopleFilter for organisations.
type OrganisationFilter struct {
	// Visible are the libraries the viewer may read; nil is every organisation, for a caller with no viewer.
	Visible    []int64
	Category   int64
	Roles      []string
	Monitored  *bool
	NamePrefix string
	// Sort is name, works or latest.
	Sort string
	Desc bool
}

// OrganisationEntry is one organisation of the list with what it did in the libraries it covers.
type OrganisationEntry struct {
	ID        int64
	Name      string
	SortName  string
	Kind      string
	Country   string
	LogoURL   string
	Monitored bool
	Roles     []string
	Works     int64
	Latest    int64
}

// OrganisationProfile is one organisation's own fields and where the viewer finds it.
type OrganisationProfile struct {
	ID        int64
	Name      string
	SortName  string
	Kind      string
	Country   string
	LogoURL   string
	Monitored bool
	// IDs maps provider to the organisation's id there.
	IDs       map[string]string
	Libraries []PersonLibrary
}

// OrganisationCredit is one work an organisation is linked to in one library, and how.
type OrganisationCredit struct {
	CategoryID int64
	Role       string
	DiscographyRow
}

// visibleOrganisation is visiblePerson for organisations.
func visibleOrganisation(idExpr string, visible []int64) sqlPart {
	if visible == nil {
		return sqlPart{sql: "1 = 1"}
	}
	member := inScope("ol.category_id", visible)
	followed := inScope("s.category_id", visible)
	return sqlPart{
		sql: "(EXISTS (SELECT 1 FROM organisation_library ol WHERE ol.organisation_id = " + idExpr + " AND " + member.sql + ")" +
			" OR EXISTS (SELECT 1 FROM subscription s WHERE s.subject_type = 'organisation' AND s.subject_id = " + idExpr +
			" AND s.enabled = 1 AND " + followed.sql + "))",
		args: append(slices.Clone(member.args), followed.args...),
	}
}

func composeOrganisations(filter OrganisationFilter, after PeopleCursor, pageSize int) (string, []any) {
	scope := peopleScope(PeopleFilter{Visible: filter.Visible, Category: filter.Category})
	category := inScope("ol.category_id", scope)
	roles := rollupRoles("ol", filter.Roles)
	rows := " FROM organisation_library ol WHERE ol.organisation_id = o.id AND " + roles.sql + " AND " + category.sql
	rowArgs := append(slices.Clone(roles.args), category.args...)
	works := sqlPart{sql: "CAST(COALESCE((SELECT SUM(ol.works)" + rows + "), 0) AS INTEGER)", args: rowArgs}
	latest := sqlPart{sql: "CAST(COALESCE((SELECT MAX(ol.latest)" + rows + "), 0) AS INTEGER)", args: rowArgs}

	var predicates store.Predicates
	if filter.Visible != nil || filter.Category != 0 || len(filter.Roles) > 0 {
		followed := inScope("s.category_id", scope)
		predicates.Add("(EXISTS (SELECT 1"+rows+") OR (o.monitored = 1 AND EXISTS (SELECT 1 FROM subscription s"+
			" WHERE s.subject_type = 'organisation' AND s.subject_id = o.id AND s.enabled = 1 AND "+followed.sql+")))",
			append(slices.Clone(rowArgs), followed.args...)...)
	}
	if filter.Monitored != nil {
		predicates.Add("o.monitored = ?", boolInt(*filter.Monitored))
	}
	if filter.NamePrefix != "" {
		upper, bounded := prefixUpperBound(filter.NamePrefix)
		if bounded {
			predicates.Add("(o.sort_name COLLATE NOCASE >= ? AND o.sort_name COLLATE NOCASE < ?)", filter.NamePrefix, upper)
		} else {
			predicates.Add("o.sort_name COLLATE NOCASE >= ?", filter.NamePrefix)
		}
	}
	direction, compare := "", ">"
	if filter.Desc {
		direction, compare = " DESC", "<"
	}
	key := sqlPart{sql: "o.sort_name COLLATE NOCASE"}
	var value any = after.Name
	switch filter.Sort {
	case PeopleByWorks:
		key, value = works, after.Value
	case PeopleByLatest:
		key, value = latest, after.Value
	}
	if after.Set {
		predicates.Add("("+key.sql+" "+compare+" ? OR ("+key.sql+" = ? AND o.id "+compare+" ?))",
			append(append(append(append(slices.Clone(key.args), value), key.args...), value), after.ID)...)
	}
	roleScope := inScope("ol.category_id", scope)
	arguments := append(slices.Clone(roleScope.args), works.args...)
	arguments = append(arguments, latest.args...)
	query := "SELECT o.id, o.name, o.sort_name, o.kind, COALESCE(o.country, ''), COALESCE(json_extract(o.fields_json, '$.logo'), ''), o.monitored," +
		"\n       COALESCE((SELECT GROUP_CONCAT(DISTINCT ol.role) FROM organisation_library ol WHERE ol.organisation_id = o.id AND ol.role <> '' AND " +
		roleScope.sql + "), ''),\n       " + works.sql + ",\n       " + latest.sql +
		"\nFROM organisation o" + predicates.Where() + "\nORDER BY " + key.sql + direction + ", o.id" + direction + "\nLIMIT ?"
	return query, append(arguments, predicates.Args(append(slices.Clone(key.args), pageSize)...)...)
}

// List answers one page of organisations: of one library, or of every library the viewer may see.
func (o Organisations) List(ctx context.Context, filter OrganisationFilter, after PeopleCursor, pageSize int) ([]OrganisationEntry, error) {
	query, arguments := composeOrganisations(filter, after, pageSize)
	rows, err := o.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("library: list organisations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []OrganisationEntry
	for rows.Next() {
		var entry OrganisationEntry
		var monitored int64
		var roles string
		if err := rows.Scan(&entry.ID, &entry.Name, &entry.SortName, &entry.Kind, &entry.Country, &entry.LogoURL, &monitored,
			&roles, &entry.Works, &entry.Latest); err != nil {
			return nil, fmt.Errorf("library: scan organisation: %w", err)
		}
		entry.Monitored = monitored == 1
		if roles != "" {
			entry.Roles = strings.Split(roles, ",")
			slices.Sort(entry.Roles)
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// Get answers one organisation as the viewer may see it; ErrOrganisationNotFound when no library they may read
// links or follows it.
func (o Organisations) Get(ctx context.Context, id int64, visible []int64) (OrganisationProfile, error) {
	reader := o.store.Read()
	rule := visibleOrganisation("organisation.id", visible)
	var seen int
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM organisation WHERE organisation.id = ? AND "+rule.sql,
		append([]any{id}, rule.args...)...).Scan(&seen); err != nil {
		return OrganisationProfile{}, fmt.Errorf("library: organisation %d: %w", id, err)
	}
	if seen == 0 {
		return OrganisationProfile{}, ErrOrganisationNotFound
	}
	queries := librarydb.New(reader)
	row, err := queries.GetOrganisation(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return OrganisationProfile{}, ErrOrganisationNotFound
	}
	if err != nil {
		return OrganisationProfile{}, fmt.Errorf("library: organisation %d: %w", id, err)
	}
	profile := OrganisationProfile{
		ID: row.ID, Name: row.Name, SortName: row.SortName, Kind: row.Kind, Country: row.Country.String,
		Monitored: row.Monitored == 1, LogoURL: logoFromFields(row.FieldsJson),
	}
	externals, err := queries.ListOrganisationExternalIDs(ctx, id)
	if err != nil {
		return OrganisationProfile{}, fmt.Errorf("library: organisation %d ids: %w", id, err)
	}
	profile.IDs = make(map[string]string, len(externals))
	for _, external := range externals {
		profile.IDs[external.Provider] = external.ExternalID
	}
	scope := inScope("category_id", visible)
	rows, err := reader.QueryContext(ctx,
		"SELECT category_id, role, works FROM organisation_library WHERE organisation_id = ? AND role <> '' AND "+scope.sql+
			" ORDER BY category_id, works DESC, role", append([]any{id}, scope.args...)...)
	if err != nil {
		return OrganisationProfile{}, fmt.Errorf("library: organisation %d libraries: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var place PersonLibrary
		if err := rows.Scan(&place.CategoryID, &place.Role, &place.Works); err != nil {
			return OrganisationProfile{}, fmt.Errorf("library: organisation %d libraries: %w", id, err)
		}
		profile.Libraries = append(profile.Libraries, place)
	}
	return profile, rows.Err()
}

// Credits answers the works an organisation is linked to in the libraries the viewer may read, newest first.
func (o Organisations) Credits(ctx context.Context, id int64, visible []int64) ([]OrganisationCredit, error) {
	scope := inScope("d.category_id", visible)
	query := `SELECT DISTINCT d.category_id, wo.role,
       ` + workColumns + `, work.primary_type,
       ` + workStateColumn + `,
       CAST((SELECT COUNT(*) FROM work_root_folder wrf WHERE wrf.work_id = work.id) AS INTEGER) AS on_disk_count,
       ` + discographyStatusColumns + `,
       ` + unverifiedColumn + ` AS unverified
FROM work_organisation wo
JOIN work ON work.id = wo.work_id
JOIN desired_item d ON d.work_id = wo.work_id AND ` + scope.sql + `
WHERE wo.organisation_id = ?
ORDER BY d.category_id, wo.role, work.year DESC, work.sort_title, work.id`
	rows, err := o.store.Read().QueryContext(ctx, query, append(slices.Clone(scope.args), id)...)
	if err != nil {
		return nil, fmt.Errorf("library: organisation %d works: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	var credits []OrganisationCredit
	for rows.Next() {
		var credit OrganisationCredit
		row := &credit.DiscographyRow
		if err := rows.Scan(&credit.CategoryID, &credit.Role,
			&row.ID, &row.MediaType, &row.CanonicalTitle, &row.SortTitle, &row.OriginalLanguage, &row.Year, &row.RuntimeMs,
			&row.AddedAt, &row.UpdatedAt, &row.AddedBy, &row.PrimaryType, &row.WantedState, &row.OnDiskCount,
			&row.PresentCount, &row.RequiredCount, &row.AvailableFrom, &row.Downloading, &row.Unverified); err != nil {
			return nil, fmt.Errorf("library: scan organisation %d work: %w", id, err)
		}
		credits = append(credits, credit)
	}
	return credits, rows.Err()
}

func logoFromFields(raw sql.NullString) string {
	if !raw.Valid {
		return ""
	}
	var fields struct {
		Logo string `json:"logo"`
	}
	if json.Unmarshal([]byte(raw.String), &fields) != nil {
		return ""
	}
	return fields.Logo
}

// OrganisationLink is an organisation a provider names for a work: found by its id there, else by kind and name.
type OrganisationLink struct {
	Kind       string
	Name       string
	Provider   string
	ExternalID string
}

// SetWorkOrganisation records a provider's answer for a work's organisation in one role, replacing the work's
// others in that role; a nil link records that the provider names none, so the work is not asked again. It answers
// the organisation and whether it was created now.
func (o Organisations) SetWorkOrganisation(
	ctx context.Context, at time.Time, workID int64, role, askedProvider string, link *OrganisationLink,
) (organisationID int64, created bool, err error) {
	err = o.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		before, err := queries.ListWorkOrganisationIDsInRole(ctx, librarydb.ListWorkOrganisationIDsInRoleParams{WorkID: workID, Role: role})
		if err != nil {
			return err
		}
		if err := queries.ClearWorkOrganisationRole(ctx, librarydb.ClearWorkOrganisationRoleParams{WorkID: workID, Role: role}); err != nil {
			return err
		}
		answer := sql.NullString{}
		if link != nil {
			answer = sql.NullString{String: link.Name, Valid: true}
			if organisationID, created, err = resolveOrganisation(ctx, queries, *link); err != nil {
				return err
			}
			if err := queries.LinkWorkOrganisation(ctx, librarydb.LinkWorkOrganisationParams{WorkID: workID, OrganisationID: organisationID, Role: role}); err != nil {
				return err
			}
		}
		if err := queries.SetWorkField(ctx, librarydb.SetWorkFieldParams{
			WorkID: workID, Field: role, Provider: askedProvider, Value: answer, FetchedAt: at.Unix(),
		}); err != nil {
			return err
		}
		return refreshOrganisations(ctx, queries, append(before, organisationID))
	})
	if err != nil {
		return 0, false, fmt.Errorf("library: %s of work %d: %w", role, workID, err)
	}
	return organisationID, created, nil
}

// resolveOrganisation finds the organisation by its provider id, else by kind and name (then remembering the id),
// and creates it when new.
func resolveOrganisation(ctx context.Context, queries *librarydb.Queries, link OrganisationLink) (int64, bool, error) {
	if link.ExternalID != "" {
		found, err := queries.GetOrganisationByExternalID(ctx, librarydb.GetOrganisationByExternalIDParams{Provider: link.Provider, ExternalID: link.ExternalID})
		if err == nil {
			return found.ID, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, false, err
		}
	}
	var id int64
	created := false
	found, err := queries.FindOrganisationByName(ctx, librarydb.FindOrganisationByNameParams{Kind: link.Kind, Name: link.Name})
	switch {
	case err == nil:
		id = found.ID
	case errors.Is(err, sql.ErrNoRows):
		made, err := queries.CreateOrganisation(ctx, librarydb.CreateOrganisationParams{Name: link.Name, SortName: link.Name, Kind: link.Kind})
		if err != nil {
			return 0, false, err
		}
		id, created = made.ID, true
	default:
		return 0, false, err
	}
	if link.ExternalID != "" {
		if err := queries.LinkOrganisationExternalID(ctx, librarydb.LinkOrganisationExternalIDParams{
			Provider: link.Provider, ExternalID: link.ExternalID, OrganisationID: id,
		}); err != nil {
			return 0, false, err
		}
	}
	return id, created, nil
}

// OrganisationDetails are what a provider says of an organisation itself; empty values are left as they are.
type OrganisationDetails struct {
	Country string
	Type    string
	Founded string
	Defunct string
}

// FillDetails keeps a provider's details of an organisation; a country already known stays.
func (o Organisations) FillDetails(ctx context.Context, organisationID int64, details OrganisationDetails) error {
	err := o.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).FillOrganisationDetails(ctx, librarydb.FillOrganisationDetailsParams{
			Country: sql.NullString{String: details.Country, Valid: details.Country != ""},
			Type:    details.Type, Founded: details.Founded, Defunct: details.Defunct, ID: organisationID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: details of organisation %d: %w", organisationID, err)
	}
	return nil
}

// WorkToAsk is a work whose organisation a provider has not been asked for yet, with the ids it is asked by.
type WorkToAsk struct {
	WorkID       int64
	ExternalID   string
	EditionLabel string
}

// WorksToAsk pages the works of a media type, by their id at externalProvider, whose organisation in role
// askedProvider was never asked for.
func (o Organisations) WorksToAsk(
	ctx context.Context, mediaType, externalProvider, role, askedProvider string, afterWorkID int64, limit int,
) ([]WorkToAsk, error) {
	rows, err := librarydb.New(o.store.Read()).ListWorksToAskOrganisation(ctx, librarydb.ListWorksToAskOrganisationParams{
		ExternalProvider: externalProvider, MediaType: mediaType, AfterID: afterWorkID, Field: role, AskedProvider: askedProvider,
		PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("library: works to ask for a %s: %w", role, err)
	}
	out := make([]WorkToAsk, len(rows))
	for i, row := range rows {
		out[i] = WorkToAsk{WorkID: row.WorkID, ExternalID: row.ExternalID, EditionLabel: row.EditionLabel}
	}
	return out, nil
}
