/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package harbor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// API is the set of Harbor operations used by the controllers. It is an
// interface so controllers can be tested against fakes.
type API interface {
	Host() string
	Username() string

	SystemInfo(ctx context.Context) (*SystemInfo, error)
	CurrentUser(ctx context.Context) (*User, error)

	GetUser(ctx context.Context, id int64) (*User, error)
	FindUser(ctx context.Context, username string) (*User, error)
	CreateUser(ctx context.Context, req UserCreate) (int64, error)
	UpdateUser(ctx context.Context, id int64, profile UserProfile) error
	SetUserPassword(ctx context.Context, id int64, password string) error
	SetUserSysAdmin(ctx context.Context, id int64, sysAdmin bool) error
	DeleteUser(ctx context.Context, id int64) error

	GetGroup(ctx context.Context, id int64) (*Group, error)
	FindGroup(ctx context.Context, name string, groupType int) (*Group, error)
	CreateGroup(ctx context.Context, g Group) (int64, error)
	UpdateGroup(ctx context.Context, id int64, name string) error
	DeleteGroup(ctx context.Context, id int64) error

	GetProject(ctx context.Context, id int64) (*Project, error)
	FindProject(ctx context.Context, name string) (*Project, error)
	CreateProject(ctx context.Context, req ProjectReq) (int64, error)
	UpdateProject(ctx context.Context, id int64, req ProjectReq) error
	DeleteProject(ctx context.Context, id int64) error

	GetProjectQuota(ctx context.Context, projectID int64) (*Quota, error)
	UpdateQuota(ctx context.Context, quotaID int64, storage int64) error

	ListMembers(ctx context.Context, projectID int64) ([]Member, error)
	AddMember(ctx context.Context, projectID int64, req MemberReq) error
	UpdateMemberRole(ctx context.Context, projectID, memberID int64, roleID int) error
	DeleteMember(ctx context.Context, projectID, memberID int64) error

	GetRobot(ctx context.Context, id int64) (*Robot, error)
	FindRobot(ctx context.Context, name string, projectID int64) (*Robot, error)
	CreateRobot(ctx context.Context, req RobotCreate) (*RobotCreated, error)
	UpdateRobot(ctx context.Context, id int64, r Robot) error
	SetRobotSecret(ctx context.Context, id int64, secret string) (string, error)
	DeleteRobot(ctx context.Context, id int64) error

	GetConfigurations(ctx context.Context) (map[string]ConfigItem, error)
	UpdateConfigurations(ctx context.Context, settings map[string]json.RawMessage) error
}

var _ API = (*Client)(nil)

// SystemInfo is the subset of /systeminfo used by the operator.
type SystemInfo struct {
	HarborVersion string `json:"harbor_version"`
	AuthMode      string `json:"auth_mode"`
}

// SystemInfo returns general information about the Harbor instance.
func (c *Client) SystemInfo(ctx context.Context) (*SystemInfo, error) {
	var info SystemInfo
	_, err := c.do(ctx, http.MethodGet, "/systeminfo", nil, nil, &info)
	return &info, err
}

// CurrentUser returns the authenticated user.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, "/users/current", nil, nil, &u)
	return &u, err
}

// ---- Users ----

// User is a Harbor user.
type User struct {
	UserID       int64  `json:"user_id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	RealName     string `json:"realname"`
	Comment      string `json:"comment"`
	SysAdminFlag bool   `json:"sysadmin_flag"`
}

// UserCreate is the payload to create a user.
type UserCreate struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	RealName string `json:"realname"`
	Comment  string `json:"comment,omitempty"`
	Password string `json:"password"`
}

// UserProfile holds the mutable user fields.
type UserProfile struct {
	Email    string `json:"email"`
	RealName string `json:"realname"`
	Comment  string `json:"comment"`
}

func (c *Client) GetUser(ctx context.Context, id int64) (*User, error) {
	var u User
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/users/%d", id), nil, nil, &u)
	return &u, err
}

// FindUser returns the user with the given username, or a 404 Error.
func (c *Client) FindUser(ctx context.Context, username string) (*User, error) {
	var users []User
	if _, err := c.do(ctx, http.MethodGet, "/users", exactQuery("username", username), nil, &users); err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Username == username {
			return &users[i], nil
		}
	}
	return nil, notFound("/users", username)
}

func (c *Client) CreateUser(ctx context.Context, req UserCreate) (int64, error) {
	h, err := c.do(ctx, http.MethodPost, "/users", nil, req, nil)
	if err != nil {
		return 0, err
	}
	return idFromLocation(h)
}

func (c *Client) UpdateUser(ctx context.Context, id int64, profile UserProfile) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/users/%d", id), nil, profile, nil)
	return err
}

func (c *Client) SetUserPassword(ctx context.Context, id int64, password string) error {
	body := map[string]string{"new_password": password}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/users/%d/password", id), nil, body, nil)
	return err
}

func (c *Client) SetUserSysAdmin(ctx context.Context, id int64, sysAdmin bool) error {
	body := map[string]bool{"sysadmin_flag": sysAdmin}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/users/%d/sysadmin", id), nil, body, nil)
	return err
}

func (c *Client) DeleteUser(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/users/%d", id), nil, nil, nil)
	return err
}

// ---- Groups ----

// Harbor user group types.
const (
	GroupTypeLDAP = 1
	GroupTypeHTTP = 2
	GroupTypeOIDC = 3
)

// Group is a Harbor user group.
type Group struct {
	ID          int64  `json:"id,omitempty"`
	GroupName   string `json:"group_name"`
	GroupType   int    `json:"group_type"`
	LDAPGroupDN string `json:"ldap_group_dn,omitempty"`
}

func (c *Client) GetGroup(ctx context.Context, id int64) (*Group, error) {
	var g Group
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/usergroups/%d", id), nil, nil, &g)
	// Harbor omits the ID when a single group is fetched.
	g.ID = id
	return &g, err
}

// FindGroup returns the group with the given name and type, or a 404 Error.
// A groupType of 0 matches any type.
func (c *Client) FindGroup(ctx context.Context, name string, groupType int) (*Group, error) {
	for page := 1; ; page++ {
		var groups []Group
		q := pageQuery(page)
		q.Set("group_name", name)
		if _, err := c.do(ctx, http.MethodGet, "/usergroups", q, nil, &groups); err != nil {
			return nil, err
		}
		for i := range groups {
			if groups[i].GroupName == name && (groupType == 0 || groups[i].GroupType == groupType) {
				return &groups[i], nil
			}
		}
		if len(groups) < pageSize {
			return nil, notFound("/usergroups", name)
		}
	}
}

func (c *Client) CreateGroup(ctx context.Context, g Group) (int64, error) {
	h, err := c.do(ctx, http.MethodPost, "/usergroups", nil, g, nil)
	if err != nil {
		return 0, err
	}
	return idFromLocation(h)
}

func (c *Client) UpdateGroup(ctx context.Context, id int64, name string) error {
	body := map[string]string{"group_name": name}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/usergroups/%d", id), nil, body, nil)
	return err
}

func (c *Client) DeleteGroup(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/usergroups/%d", id), nil, nil, nil)
	return err
}

// ---- Projects ----

// CVEItem is a single allowed CVE.
type CVEItem struct {
	CVEID string `json:"cve_id"`
}

// CVEAllowlist is a Harbor CVE allowlist. ExpiresAt is a Unix timestamp.
type CVEAllowlist struct {
	Items     []CVEItem `json:"items"`
	ExpiresAt *int64    `json:"expires_at,omitempty"`
}

// Project is a Harbor project. Metadata values are strings such as "true".
type Project struct {
	ProjectID    int64             `json:"project_id"`
	Name         string            `json:"name"`
	Metadata     map[string]string `json:"metadata"`
	CVEAllowlist *CVEAllowlist     `json:"cve_allowlist,omitempty"`
}

// ProjectReq is the payload to create or update a project.
type ProjectReq struct {
	ProjectName  string            `json:"project_name,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CVEAllowlist *CVEAllowlist     `json:"cve_allowlist,omitempty"`
	StorageLimit *int64            `json:"storage_limit,omitempty"`
}

func (c *Client) GetProject(ctx context.Context, id int64) (*Project, error) {
	var p Project
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d", id), nil, nil, &p)
	return &p, err
}

// FindProject returns the project with the given name, or a 404 Error.
func (c *Client) FindProject(ctx context.Context, name string) (*Project, error) {
	var projects []Project
	if _, err := c.do(ctx, http.MethodGet, "/projects", exactQuery("name", name), nil, &projects); err != nil {
		return nil, err
	}
	for i := range projects {
		if projects[i].Name == name {
			return &projects[i], nil
		}
	}
	return nil, notFound("/projects", name)
}

func (c *Client) CreateProject(ctx context.Context, req ProjectReq) (int64, error) {
	h, err := c.do(ctx, http.MethodPost, "/projects", nil, req, nil)
	if err != nil {
		return 0, err
	}
	return idFromLocation(h)
}

func (c *Client) UpdateProject(ctx context.Context, id int64, req ProjectReq) error {
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d", id), nil, req, nil)
	return err
}

func (c *Client) DeleteProject(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%d", id), nil, nil, nil)
	return err
}

// ---- Quotas ----

// Quota is a Harbor resource quota. Storage values are bytes; -1 is unlimited.
type Quota struct {
	ID   int64            `json:"id"`
	Hard map[string]int64 `json:"hard"`
	Used map[string]int64 `json:"used"`
}

// GetProjectQuota returns the quota of a project.
func (c *Client) GetProjectQuota(ctx context.Context, projectID int64) (*Quota, error) {
	var quotas []Quota
	q := url.Values{"reference": {"project"}, "reference_id": {strconv.FormatInt(projectID, 10)}}
	if _, err := c.do(ctx, http.MethodGet, "/quotas", q, nil, &quotas); err != nil {
		return nil, err
	}
	if len(quotas) == 0 {
		return nil, notFound("/quotas", strconv.FormatInt(projectID, 10))
	}
	return &quotas[0], nil
}

func (c *Client) UpdateQuota(ctx context.Context, quotaID int64, storage int64) error {
	body := map[string]map[string]int64{"hard": {"storage": storage}}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/quotas/%d", quotaID), nil, body, nil)
	return err
}

// ---- Project members ----

// Harbor project role IDs.
const (
	RoleProjectAdmin = 1
	RoleDeveloper    = 2
	RoleGuest        = 3
	RoleMaintainer   = 4
	RoleLimitedGuest = 5
)

// Member entity types.
const (
	EntityTypeUser  = "u"
	EntityTypeGroup = "g"
)

// Member is a project member.
type Member struct {
	ID         int64  `json:"id"`
	EntityName string `json:"entity_name"`
	EntityType string `json:"entity_type"`
	EntityID   int64  `json:"entity_id"`
	RoleID     int    `json:"role_id"`
}

// MemberUser identifies a user member by name.
type MemberUser struct {
	Username string `json:"username"`
}

// MemberGroup identifies a group member by ID.
type MemberGroup struct {
	ID int64 `json:"id"`
}

// MemberReq is the payload to add a project member.
type MemberReq struct {
	RoleID      int          `json:"role_id"`
	MemberUser  *MemberUser  `json:"member_user,omitempty"`
	MemberGroup *MemberGroup `json:"member_group,omitempty"`
}

func (c *Client) ListMembers(ctx context.Context, projectID int64) ([]Member, error) {
	var all []Member
	for page := 1; ; page++ {
		var members []Member
		q := pageQuery(page)
		if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d/members", projectID), q, nil, &members); err != nil {
			return nil, err
		}
		all = append(all, members...)
		if len(members) < pageSize {
			return all, nil
		}
	}
}

func (c *Client) AddMember(ctx context.Context, projectID int64, req MemberReq) error {
	_, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/members", projectID), nil, req, nil)
	return err
}

func (c *Client) UpdateMemberRole(ctx context.Context, projectID, memberID int64, roleID int) error {
	body := map[string]int{"role_id": roleID}
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/members/%d", projectID, memberID), nil, body, nil)
	return err
}

func (c *Client) DeleteMember(ctx context.Context, projectID, memberID int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/projects/%d/members/%d", projectID, memberID), nil, nil, nil)
	return err
}

// ---- Robots ----

// RobotAccess is a resource/action pair.
type RobotAccess struct {
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Effect   string `json:"effect,omitempty"`
}

// RobotPermission grants access on a namespace.
type RobotPermission struct {
	Kind      string        `json:"kind"`
	Namespace string        `json:"namespace"`
	Access    []RobotAccess `json:"access"`
}

// Robot is a Harbor robot account. Name is the full name including the prefix.
type Robot struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Level       string            `json:"level"`
	Duration    int64             `json:"duration"`
	Disable     bool              `json:"disable"`
	ExpiresAt   int64             `json:"expires_at"`
	Permissions []RobotPermission `json:"permissions"`
}

// RobotCreate is the payload to create a robot. Name excludes the prefix.
type RobotCreate struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Level       string            `json:"level"`
	Duration    int64             `json:"duration"`
	Disable     bool              `json:"disable"`
	Permissions []RobotPermission `json:"permissions"`
}

// RobotCreated is returned when a robot is created.
type RobotCreated struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Secret    string `json:"secret"`
	ExpiresAt int64  `json:"expires_at"`
}

func (c *Client) GetRobot(ctx context.Context, id int64) (*Robot, error) {
	var r Robot
	_, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/robots/%d", id), nil, nil, &r)
	return &r, err
}

// FindRobot returns the robot whose full name is "<prefix><name>", where name
// is "<robot>" for system robots and "<project>+<robot>" for project robots.
// Harbor lists system and project robots separately, so projectID selects
// the robots of that project; 0 selects system robots. The prefix is
// configurable in Harbor (default "robot$"), so any prefix ending in "$" is
// accepted.
func (c *Client) FindRobot(ctx context.Context, name string, projectID int64) (*Robot, error) {
	filter := "Level=system"
	if projectID != 0 {
		filter = fmt.Sprintf("Level=project,ProjectID=%d", projectID)
	}
	for page := 1; ; page++ {
		var robots []Robot
		q := pageQuery(page)
		q.Set("q", filter)
		if _, err := c.do(ctx, http.MethodGet, "/robots", q, nil, &robots); err != nil {
			return nil, err
		}
		for i := range robots {
			if RobotNameMatches(robots[i].Name, name) {
				return &robots[i], nil
			}
		}
		if len(robots) < pageSize {
			return nil, notFound("/robots", name)
		}
	}
}

// RobotNameMatches reports whether fullName is name with a robot prefix.
func RobotNameMatches(fullName, name string) bool {
	return fullName == name || strings.HasSuffix(fullName, "$"+name)
}

func (c *Client) CreateRobot(ctx context.Context, req RobotCreate) (*RobotCreated, error) {
	var out RobotCreated
	_, err := c.do(ctx, http.MethodPost, "/robots", nil, req, &out)
	return &out, err
}

func (c *Client) UpdateRobot(ctx context.Context, id int64, r Robot) error {
	r.ID = id
	_, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/robots/%d", id), nil, r, nil)
	return err
}

// SetRobotSecret sets the robot secret. When secret is empty Harbor generates
// a new one. The effective secret is returned.
func (c *Client) SetRobotSecret(ctx context.Context, id int64, secret string) (string, error) {
	var out struct {
		Secret string `json:"secret"`
	}
	body := map[string]string{}
	if secret != "" {
		body["secret"] = secret
	}
	if _, err := c.do(ctx, http.MethodPatch, fmt.Sprintf("/robots/%d", id), nil, body, &out); err != nil {
		return "", err
	}
	if out.Secret == "" {
		out.Secret = secret
	}
	return out.Secret, nil
}

func (c *Client) DeleteRobot(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/robots/%d", id), nil, nil, nil)
	return err
}

// ---- Configurations ----

// ConfigItem is a system configuration value. Harbor does not return
// credentials such as ldap_search_password or oidc_client_secret.
type ConfigItem struct {
	Value    json.RawMessage `json:"value"`
	Editable bool            `json:"editable"`
}

// GetConfigurations returns the system configuration keyed by setting name.
func (c *Client) GetConfigurations(ctx context.Context) (map[string]ConfigItem, error) {
	var out map[string]ConfigItem
	_, err := c.do(ctx, http.MethodGet, "/configurations", nil, nil, &out)
	return out, err
}

// UpdateConfigurations sets the given configuration keys; others are unchanged.
func (c *Client) UpdateConfigurations(ctx context.Context, settings map[string]json.RawMessage) error {
	_, err := c.do(ctx, http.MethodPut, "/configurations", nil, settings, nil)
	return err
}

func notFound(p, name string) error {
	return &Error{StatusCode: http.StatusNotFound, Method: http.MethodGet, Path: p, Message: fmt.Sprintf("%q not found", name)}
}
