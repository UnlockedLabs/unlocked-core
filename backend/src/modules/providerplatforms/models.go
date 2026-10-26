package providerplatforms

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

// Provider Platforms' tables as Go types. The JSON names are the ones core
// sent before the move (EN-193), so the frontend sees no change.

// DatabaseFields are the columns most of Provider Platforms' tables share. The
// create and update user come from the identity.Claims in the query's context.
type DatabaseFields struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	CreateUserID *uint `json:"create_user_id"`
	UpdateUserID *uint `json:"update_user_id"`
}

func (m *DatabaseFields) BeforeCreate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		m.CreateUserID = &c.UserID
	}
	return nil
}

func (m *DatabaseFields) BeforeUpdate(tx *gorm.DB) error {
	if c, ok := identity.FromContext(tx.Statement.Context); ok {
		m.UpdateUserID = &c.UserID
		tx.Statement.SetColumn("update_user_id", c.UserID)
	}
	return nil
}

// ProviderPlatformType is api.ProviderType, so core and the module share one
// list of provider types.
type ProviderPlatformType = api.ProviderType

const (
	CanvasOSS   = api.CanvasOSS
	CanvasCloud = api.CanvasCloud
	Kolibri     = api.Kolibri
	Brightspace = api.Brightspace
	EssentialEd = api.EssentialEd
)

type ProviderPlatformState string

const (
	Enabled   ProviderPlatformState = "enabled"
	Disabled  ProviderPlatformState = "disabled"
	Archived  ProviderPlatformState = "archived"
	UuidV4Len                       = 32
)

const (
	BrightspaceRedirectEndpointURL string = "/api/provider-platforms/callback"
	BrightspaceAuthorizationURL    string = "https://auth.brightspace.com/oauth2/auth"
	BrightspaceTokenURL            string = "https://auth.brightspace.com/core/connect/token"
	BrightspaceScopes              string = "accommodations:profile:read attributes:schemas:read datahub:dataexports:download,read datasets:bds:read organizations:image:read organizations:organization:read orgunits:course:read role:detail:read users:own_profile:read users:own_pronoun:read"
	BrightspaceApiVersion          string = "1.31"
)

type ProviderPlatform struct {
	DatabaseFields
	Type                   ProviderPlatformType  `gorm:"size:100"                    json:"type"`
	Name                   string                `gorm:"size:255"                    json:"name"`
	AccountID              string                `gorm:"size:64"                     json:"account_id"`
	AccessKey              string                `gorm:"size:255"                    json:"access_key"`
	BaseUrl                string                `gorm:"size:255"                    json:"base_url"`
	State                  ProviderPlatformState `gorm:"size:100"                    json:"state"`
	ExternalAuthProviderId string                `gorm:"size:128"                    json:"external_auth_provider_id"`
	/* this field needs to be fetched by joining oidc_clients when querying the provider_platforms */
	OidcID uint `gorm:"-" json:"oidc_id"`

	Courses              []Course              `gorm:"foreignKey:ProviderPlatformID;references:ID" json:"-"`
	ProviderUserMappings []ProviderUserMapping `gorm:"foreignKey:ProviderPlatformID;references:ID" json:"-"`
	OidcClient           *OidcClient           `gorm:"foreignKey:ProviderPlatformID;references:ID" json:"-"`
}

func (ProviderPlatform) TableName() string {
	return "provider_platforms"
}

func (provider *ProviderPlatform) BeforeCreate(tx *gorm.DB) (err error) {
	if err := provider.DatabaseFields.BeforeCreate(tx); err != nil {
		return err
	}
	provider.UpdateUserID = nil
	provider.BaseUrl = strings.TrimSpace(provider.BaseUrl)

	if provider.Type == Kolibri && !strings.Contains(provider.AccountID, "-") && len(provider.AccountID) == UuidV4Len {
		// convert the uuid back into hypenated format
		provider.AccountID = fmt.Sprintf("%s-%s-%s-%s-%s", provider.AccountID[0:8], provider.AccountID[8:12], provider.AccountID[12:16], provider.AccountID[16:20], provider.AccountID[20:])
	}
	return nil
}

func (provider *ProviderPlatform) BeforeUpdate(tx *gorm.DB) (err error) {
	if err := provider.DatabaseFields.BeforeUpdate(tx); err != nil {
		return err
	}
	provider.BaseUrl = strings.TrimSpace(provider.BaseUrl)
	return nil
}

func (provider *ProviderPlatform) AfterFind(tx *gorm.DB) (err error) {
	if key, keyErr := DecryptAccessKey(provider.AccessKey); keyErr == nil {
		provider.AccessKey = strings.TrimSpace(key)
	}
	// A pasted base URL often carries surrounding whitespace, which makes
	// url.Parse read the leading space as a path segment and reject the scheme.
	provider.BaseUrl = strings.TrimSpace(provider.BaseUrl)
	return nil
}

func DecryptAccessKey(axxKey string) (string, error) {
	key := os.Getenv("APP_KEY")
	hashedKey := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hashedKey[:])
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(axxKey)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < aes.BlockSize {
		return "", err
	}
	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]
	//nolint:staticcheck // CFB mode preserved for backward compatibility with existing encrypted data
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(ciphertext, ciphertext)
	return string(ciphertext), nil
}

func EncryptAccessKey(apiKey string) (string, error) {
	key := os.Getenv("APP_KEY")
	hashedKey := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(hashedKey[:])
	if err != nil {
		return "", err
	}
	ciphertext := make([]byte, aes.BlockSize+len(apiKey))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return "", err
	}
	//nolint:staticcheck // CFB mode preserved for backward compatibility with existing encrypted data
	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(ciphertext[aes.BlockSize:], []byte(apiKey))
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	return encoded, nil
}

func (provider *ProviderPlatform) GetDefaultRedirectURI() []string {
	switch provider.Type {
	case CanvasOSS, CanvasCloud:
		return []string{provider.BaseUrl + "/login/oauth2/callback"}

	case Kolibri:
		defaultUri := provider.BaseUrl + "/oidccallback/"
		stripped := strings.Replace(defaultUri, "https", "http", 1)
		return []string{defaultUri, stripped}
	}
	return []string{}
}

// oauth2 config is currently only for the Brightspace provider, implemented a switch for other oauth2 providers to be added in the future
func (provider *ProviderPlatform) GetOAuth2Config() *oauth2.Config {
	var (
		config oauth2.Config
		secret string
	)
	switch provider.Type {
	case Brightspace:
		if strings.Contains(provider.AccessKey, ";") {
			secret = strings.Split(provider.AccessKey, ";")[0]
		} else {
			secret = provider.AccessKey
		}
		config = oauth2.Config{
			ClientID:     provider.AccountID,
			ClientSecret: secret,
			RedirectURL:  os.Getenv("APP_URL") + BrightspaceRedirectEndpointURL,
			Scopes:       []string{BrightspaceScopes},
			Endpoint: oauth2.Endpoint{
				AuthURL:   BrightspaceAuthorizationURL,
				TokenURL:  BrightspaceTokenURL,
				AuthStyle: oauth2.AuthStyleInHeader,
			},
		}
	}
	return &config
}

func CreateOAuth2UrlState() string {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil { //if err then handle with custom generator
		randomBytes = generateRandomString()
	}
	return base64.URLEncoding.EncodeToString(randomBytes)
}

func generateRandomString() []byte {
	const lettersAndNumbers = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	randomBytes := make([]byte, 32)
	generator := mrand.New(mrand.NewSource(time.Now().UnixNano())) // seed it
	for idx := range randomBytes {
		randomBytes[idx] = randomBytes[generator.Intn(len(lettersAndNumbers))]
	}
	return randomBytes
}

type AuthProviderStatus string

const (
	OpenIDConnect AuthProviderStatus = "openid_connect"
	Oauth2        AuthProviderStatus = "oauth2"
	None          AuthProviderStatus = "none"
)

type ProviderUserMapping struct {
	DatabaseFields
	UserID                       uint               `gorm:"not null" json:"user_id"`
	ProviderPlatformID           uint               `gorm:"not null" json:"provider_platform_id"`
	ExternalUserID               string             `gorm:"size:255;not null" json:"external_user_id"`
	ExternalUsername             string             `gorm:"size:255;not null" json:"external_username"`
	AuthenticationProviderStatus AuthProviderStatus `gorm:"size:255;not null;default:none" json:"authentication_provider_status"`
	ExternalLoginID              *string            `gorm:"size:255" json:"external_login_id"`

	/*    Relations    */
	ProviderPlatform *ProviderPlatform `gorm:"foreignKey:ProviderPlatformID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

func (ProviderUserMapping) TableName() string {
	return "provider_user_mappings"
}

// MappedUserResponse is returned by the mapped-users endpoint.
// It embeds the local user and adds canvas display-name fields
// that are resolved live from the provider API (never stored in the DB).
type MappedUserResponse struct {
	user
	CanvasNameFirst string `json:"canvas_name_first"`
	CanvasNameLast  string `json:"canvas_name_last"`
}

type OidcClient struct {
	DatabaseFields
	ProviderPlatformID uint   `json:"provider_platform_id"`
	ClientID           string `gorm:"size:255" json:"client_id"`
	ClientName         string `gorm:"size:255" json:"client_name"`
	ClientSecret       string `gorm:"size:255" json:"client_secret"`
	RedirectURIs       string `gorm:"size:255" json:"redirect_uris"`
	Scopes             string `gorm:"size:255" json:"scope"`

	Provider *ProviderPlatform `gorm:"foreignKey:ProviderPlatformID" json:"-"`
}

type ClientResponse struct {
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	Scopes        string `json:"scope"`
	AuthEndpoint  string `json:"auth_url"`
	TokenEndpoint string `json:"token_url"`
}

const DefaultScopes = "openid offline profile email"

func (OidcClient) TableName() string {
	return "oidc_clients"
}

func OidcClientFromProvider(prov *ProviderPlatform, autoRegister bool, client *http.Client) (*OidcClient, string, error) {
	externalId := ""
	redirectURI := prov.GetDefaultRedirectURI()
	headers := map[string]string{}
	headers["Authorization"] = "Bearer " + os.Getenv("ORY_TOKEN")
	headers["Origin"] = os.Getenv("APP_URL")
	body := map[string]interface{}{}
	body["client_name"] = prov.Name
	body["client_uri"] = prov.BaseUrl
	body["redirect_uris"] = redirectURI
	body["scope"] = DefaultScopes
	body["acces_token_strategy"] = "opaque"
	body["metadata"] = map[string]interface{}{
		"Origin": os.Getenv("APP_URL"),
	}
	body["response_types"] = []string{"code", "id_token", "token"}
	body["allowed_cors_origins"] = []string{os.Getenv("APP_URL"), prov.BaseUrl, os.Getenv("HYDRA_PUBLIC_URL"), "http://127.0.0.1"}
	body["grant_types"] = []string{"authorization_code", "refresh_token"}
	body["authorization_code_grant_access_token_lifespan"] = "3h"
	body["authorization_code_grant_id_token_lifespan"] = "3h"
	body["authorization_code_grant_refresh_token_lifespan"] = "3h"
	body["skip_consent"] = true
	body["skip_logout_consent"] = true
	switch prov.Type {
	case CanvasCloud, CanvasOSS:
		body["token_endpoint_auth_method"] = "client_secret_post"
		body["subject_type"] = "pairwise"
	case Kolibri:
		body["token_endpoint_auth_method"] = "client_secret_basic"
		body["subject_type"] = "public"
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, externalId, err
	}
	req, err := http.NewRequest("POST", os.Getenv("HYDRA_ADMIN_URL")+"/admin/clients", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, externalId, err
	}
	req.Header.Add("Authorization", headers["Authorization"])
	resp, err := client.Do(req)
	if err != nil {
		return nil, externalId, err
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.Error("Error closing response body")
		}
	}()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, externalId, fmt.Errorf("error creating client in hydra oidc server: received %s", resp.Status)
	}
	var clientData map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&clientData)
	if err != nil {
		return nil, externalId, err
	}
	if clientData == nil {
		return nil, externalId, fmt.Errorf("error creating client: %s", resp.Status)
	}
	joined := strings.Join(redirectURI, ",")
	oidcClient := &OidcClient{
		ProviderPlatformID: prov.ID,
		ClientID:           clientData["client_id"].(string),
		ClientName:         clientData["client_name"].(string),
		ClientSecret:       clientData["client_secret"].(string),
		RedirectURIs:       joined,
		Scopes:             clientData["scope"].(string),
	}
	if autoRegister && (prov.Type == CanvasCloud || prov.Type == CanvasOSS) {
		externalId, err = autoRegisterCanvas(prov, oidcClient)
		if err != nil {
			log.Error("Error auto registering provider as client: ", err)
		}
	}
	return oidcClient, externalId, nil
}

func autoRegisterCanvas(prov *ProviderPlatform, oidcClient *OidcClient) (string, error) {
	client := http.Client{}
	externalId := ""
	// register the login client with canvas
	baseURL := prov.BaseUrl + "/api/v1/accounts/" + prov.AccountID + "/authentication_providers"
	// we need to add the client_id, client_secret, and redirect_uri to the request form encoded
	form := url.Values{}
	form.Add("auth_type", "openid_connect")
	form.Add("client_id", oidcClient.ClientID)
	form.Add("client_secret", oidcClient.ClientSecret)
	form.Add("authorize_url", os.Getenv("APP_URL")+"/oauth2/auth")
	form.Add("token_url", os.Getenv("APP_URL")+"/oauth2/token")
	form.Add("userinfo_endpoint", os.Getenv("APP_URL")+"/userinfo")
	form.Add("login_attribute", "preferred_username")
	request, err := http.NewRequest("POST", baseURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		log.Println("Error creating request object: ", err)
		return "", err
	}
	log.Printf("Authorization: Bearer %s", prov.AccessKey)
	headers := make(map[string]string)
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	headers["Authorization"] = "Bearer " + prov.AccessKey
	headers["Accept"] = "application/json"
	for k, v := range headers {
		request.Header.Add(k, v)
	}
	log.Printf("Request: %v", request)
	response, err := client.Do(request)
	if err != nil {
		log.Println("Error sending request: ", err)
		return "", err
	}
	defer func() {
		if response.Body.Close() != nil {
			log.Println("Error closing response body")
		}
	}()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		log.Println("Error creating authentication provider: ", response.Status)
	}
	var authProvider map[string]interface{}
	err = json.NewDecoder(response.Body).Decode(&authProvider)
	if err != nil {
		log.Error("Error decoding response body: ", err)
		return "", err
	}
	if authProvider == nil {
		log.Error("Error creating authentication provider: ", response.Status, response.Body)
		return "", err
	}
	if authProvider["id"] == nil {
		log.Error("Error creating authentication provider: no ID in response")
		return "", err
	}
	externalId = fmt.Sprintf("%d", int(authProvider["id"].(float64)))
	log.Info("new external id registered from canvas: " + externalId)
	return externalId, nil
}

type Course struct {
	DatabaseFields
	ProviderPlatformID      uint       `gorm:"not null" json:"provider_platform_id"`
	Name                    string     `gorm:"size:60" json:"name"`
	Description             *string    `gorm:"size:510" json:"description"`
	ExternalID              string     `gorm:"size:255" json:"external_id"` // kolibri: root, canvas: course_id
	ThumbnailURL            *string    `gorm:"size:255" json:"thumbnail_url"`
	Type                    CourseType `gorm:"size:255" json:"type"`
	OutcomeTypes            *string    `gorm:"size:255" json:"outcome_types"`
	ExternalURL             *string    `gorm:"size:255" json:"external_url"`
	AltName                 *string    `gorm:"size:255" json:"alt_name"`
	TotalProgressMilestones uint       `json:"total_progress_milestones"`
	StartDt                 *time.Time `gorm:"type:date" json:"start_dt"`
	EndDt                   *time.Time `gorm:"type:date" json:"end_dt"`

	ProviderPlatform *ProviderPlatform `gorm:"foreignKey:ProviderPlatformID;constraint:OnDelete SET NULL" json:"-"`
	Enrollments      []UserEnrollment  `gorm:"foreignKey:CourseID;constraint:OnDelete SET NULL" json:"-"`
	Milestones       []Milestone       `gorm:"foreignKey:CourseID;constraint:OnDelete SET NULL" json:"-"`
	Outcomes         []Outcome         `gorm:"foreignKey:CourseID;constraint:OnDelete SET NULL" json:"-"`
}

type CourseType string

const (
	OpenEnrollment  CourseType = "open_enrollment"
	OpenContent     CourseType = "open_content"
	FixedEnrollment CourseType = "fixed_enrollment"
)

func (Course) TableName() string {
	return "courses"
}

type UserEnrollment struct {
	UserID     uint       `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	CourseID   uint       `json:"course_id" gorm:"primaryKey;autoIncrement:false"`
	ExternalID *string    `json:"external_id" gorm:"size:64"`
	CreatedAt  *time.Time `json:"created_at"`
	UpdatedAt  *time.Time `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at"`

	Course *Course `json:"course,omitempty" gorm:"foreignKey:CourseID;constraint:OnDelete CASCADE"`
}

func (UserEnrollment) TableName() string { return "user_enrollments" }

type MilestoneType string

const (
	Enrollment           MilestoneType = "enrollment"
	QuizSubmission       MilestoneType = "quiz_submission"
	AssignmentSubmission MilestoneType = "assignment_submission"
	GradeReceived        MilestoneType = "grade_received"
	DiscussionPost       MilestoneType = "discussion_post"
)

type Milestone struct {
	DatabaseFields               // ID, CreatedAt, UpdatedAt, DeletedAt
	UserID         uint          `gorm:"not null" json:"user_id"`
	CourseID       uint          `gorm:"not null" json:"course_id"`
	ExternalID     string        `gorm:"size:255;not null;unique" json:"external_id"`
	Type           MilestoneType `gorm:"size:255;not null" json:"type"`
	IsCompleted    bool          `gorm:"default:false" json:"is_completed"`

	Course *Course `gorm:"foreignKey:CourseID" json:"-"`
}

func (Milestone) TableName() string {
	return "milestones"
}

type Outcome struct {
	DatabaseFields
	Type     OutcomeType `gorm:"size:255;not null" json:"type"`
	CourseID uint        `gorm:"not null" json:"course_id"`
	UserID   uint        `gorm:"not null" json:"user_id"`
	Value    string      `gorm:"size:255" json:"value"`

	Course *Course `gorm:"foreignKey:CourseID" json:"-"`
}

type OutcomeType string

const (
	Certificate        OutcomeType = "certificate"
	CourseCompletion   OutcomeType = "grade"
	ProgressCompletion OutcomeType = "progress_completion"
	PathwayCompletion  OutcomeType = "pathway_completion"
	CollegeCredit      OutcomeType = "college_credit"
)

type ActivityType string

const (
	CourseInteraction  ActivityType = "interaction" // watching video, clicking link, etc
	ContentInteraction ActivityType = "completion"  // completing submodule, video, etc
)

type Activity struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
	CourseID  uint           `gorm:"not null" json:"course_id"`
	UserID    uint           `gorm:"not null" json:"user_id"`
	Type      ActivityType   `gorm:"size:255;not null" json:"type"`
	TotalTime int64          `json:"total_time"`
	TimeDelta int64          `json:"time_delta"`

	// is this a url perhaps?
	ExternalID string `gorm:"size:255;not null" json:"external_content_id"`

	Course *Course `gorm:"foreignKey:CourseID" json:"-"`
}

func (Activity) TableName() string {
	return "activities"
}

type UserCourseActivityTotals struct {
	UserID    uint       `gorm:"primaryKey" json:"user_id"`
	CourseID  uint       `gorm:"primaryKey" json:"course_id"`
	TotalTime int64      `json:"total_time"`
	LastTS    *time.Time `json:"last_ts"`
}

func (UserCourseActivityTotals) TableName() string {
	return "user_course_activity_totals"
}

type DailyActivity struct {
	Date       time.Time  `json:"date"`
	TotalTime  uint       `json:"total_time"`
	Quartile   uint       `json:"quartile"`
	Activities []Activity `json:"activities"`
}

// ImportUser is a user account at a provider platform, as provider-middleware
// and the live providers report it.
type ImportUser struct {
	Username         string `json:"username"`
	NameFirst        string `json:"name_first"`
	NameLast         string `json:"name_last"`
	Email            string `json:"email"`
	ExternalUserID   string `json:"external_user_id"`
	ExternalUsername string `json:"external_username"`
}

// user is a row of core's users table, which Provider Platforms reads to
// match and list the users linked to a provider. Core owns the table; the
// JSON is core's, so responses don't change.
type user struct {
	DatabaseFields
	Username      string     `json:"username"`
	NameFirst     string     `json:"name_first"`
	Email         string     `json:"email"`
	NameLast      string     `json:"name_last"`
	Role          users.Role `json:"role"`
	KratosID      *string    `json:"kratos_id"`
	FacilityID    uint       `json:"facility_id"`
	DocID         *string    `json:"doc_id"`
	DeactivatedAt *time.Time `json:"deactivated_at,omitempty"`
}

func (user) TableName() string { return "users" }

// cronJob and runnableTask are rows of core's cron_jobs and runnable_tasks
// tables, which hold the LMS jobs' bookkeeping: one cron_jobs row per job,
// and one runnable_tasks row per job and provider with its status and the
// time of its last successful run (jobs.go).
type cronJob struct {
	ID        string `gorm:"primaryKey"`
	Name      string
	Schedule  string
	Category  int
	CreatedAt time.Time
}

func (cronJob) TableName() string { return "cron_jobs" }

type runnableTask struct {
	ID                 uint `gorm:"primaryKey"`
	JobID              string
	LastRun            time.Time
	ProviderPlatformID *uint
	Status             taskStatus
}

func (runnableTask) TableName() string { return "runnable_tasks" }

type taskStatus string

const (
	// providerPlatformJob is the cron_jobs category of the LMS jobs.
	providerPlatformJob = 1

	taskPending taskStatus = "pending"
	taskRunning taskStatus = "running"
)

func stringPtr(s string) *string { return &s }

// nilIfBlank returns nil for empty or whitespace-only strings so they are
// stored as NULL.
func nilIfBlank(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
