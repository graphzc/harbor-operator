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

// Package harbortest provides an in-memory fake of the Harbor REST API for tests.
package harbortest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/graphzc/harbor-operator/internal/harbor"
)

const prefix = "/api/v2.0"

// Server is a fake Harbor. Its exported maps may be inspected by tests while
// holding Mu.
type Server struct {
	*httptest.Server

	Mu            sync.Mutex
	AdminUser     string
	AdminPassword string
	Users         map[int64]*harbor.User
	Passwords     map[int64]string
	Groups        map[int64]*harbor.Group
	Projects      map[int64]*harbor.Project
	Quotas        map[int64]*harbor.Quota // keyed by project ID
	Members       map[int64][]harbor.Member
	Robots        map[int64]*harbor.Robot
	RobotSecrets  map[int64]string
	nextID        int64
}

// NewServer starts a fake Harbor with an "admin" system administrator.
func NewServer(adminPassword string) *Server {
	s := &Server{
		AdminUser:     "admin",
		AdminPassword: adminPassword,
		Users:         map[int64]*harbor.User{1: {UserID: 1, Username: "admin", SysAdminFlag: true}},
		Passwords:     map[int64]string{1: adminPassword},
		Groups:        map[int64]*harbor.Group{},
		Projects:      map[int64]*harbor.Project{},
		Quotas:        map[int64]*harbor.Quota{},
		Members:       map[int64][]harbor.Member{},
		Robots:        map[int64]*harbor.Robot{},
		RobotSecrets:  map[int64]string{},
		nextID:        100,
	}
	mux := http.NewServeMux()
	s.routes(mux)
	s.Server = httptest.NewServer(s.auth(mux))
	return s
}

func (s *Server) id() int64 {
	s.nextID++
	return s.nextID
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == prefix+"/systeminfo" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		s.Mu.Lock()
		valid := ok && u == s.AdminUser && p == s.AdminPassword
		s.Mu.Unlock()
		if !valid {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes(mux *http.ServeMux) {
	h := func(pattern string, fn func(w http.ResponseWriter, r *http.Request)) {
		method, p, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" "+prefix+p, func(w http.ResponseWriter, r *http.Request) {
			s.Mu.Lock()
			defer s.Mu.Unlock()
			fn(w, r)
		})
	}

	h("GET /systeminfo", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, harbor.SystemInfo{HarborVersion: "v2.13.0-fake", AuthMode: "db_auth"})
	})
	h("GET /users/current", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.Users[1])
	})

	// Users
	h("GET /users", func(w http.ResponseWriter, r *http.Request) {
		name := exactQ(r, "username")
		out := []harbor.User{}
		for _, u := range s.Users {
			if name == "" || u.Username == name {
				out = append(out, *u)
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	h("POST /users", func(w http.ResponseWriter, r *http.Request) {
		var req harbor.UserCreate
		if !decode(w, r, &req) {
			return
		}
		for _, u := range s.Users {
			if u.Username == req.Username {
				writeErr(w, http.StatusConflict, "username already exists")
				return
			}
		}
		id := s.id()
		s.Users[id] = &harbor.User{UserID: id, Username: req.Username, Email: req.Email, RealName: req.RealName, Comment: req.Comment}
		s.Passwords[id] = req.Password
		created(w, "/users", id)
	})
	h("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		if u, ok := s.Users[pathID(r)]; ok {
			writeJSON(w, http.StatusOK, u)
			return
		}
		writeErr(w, http.StatusNotFound, "user not found")
	})
	h("PUT /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.Users[pathID(r)]
		if !ok {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		var p harbor.UserProfile
		if !decode(w, r, &p) {
			return
		}
		u.Email, u.RealName, u.Comment = p.Email, p.RealName, p.Comment
		w.WriteHeader(http.StatusOK)
	})
	h("PUT /users/{id}/password", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if _, ok := s.Users[id]; !ok {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		var body map[string]string
		if !decode(w, r, &body) {
			return
		}
		if s.Passwords[id] == body["new_password"] {
			writeErr(w, http.StatusBadRequest, "the new password can not be same with the old one")
			return
		}
		s.Passwords[id] = body["new_password"]
		w.WriteHeader(http.StatusOK)
	})
	h("PUT /users/{id}/sysadmin", func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.Users[pathID(r)]
		if !ok {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		var body map[string]bool
		if !decode(w, r, &body) {
			return
		}
		u.SysAdminFlag = body["sysadmin_flag"]
		w.WriteHeader(http.StatusOK)
	})
	h("DELETE /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if _, ok := s.Users[id]; !ok {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		delete(s.Users, id)
		delete(s.Passwords, id)
		w.WriteHeader(http.StatusOK)
	})

	// Groups
	h("GET /usergroups", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("group_name")
		out := []harbor.Group{}
		for _, g := range s.Groups {
			if name == "" || strings.Contains(g.GroupName, name) {
				out = append(out, *g)
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	h("POST /usergroups", func(w http.ResponseWriter, r *http.Request) {
		var g harbor.Group
		if !decode(w, r, &g) {
			return
		}
		for _, e := range s.Groups {
			if e.GroupName == g.GroupName && e.GroupType == g.GroupType {
				writeErr(w, http.StatusConflict, "group already exists")
				return
			}
		}
		g.ID = s.id()
		s.Groups[g.ID] = &g
		created(w, "/usergroups", g.ID)
	})
	h("GET /usergroups/{id}", func(w http.ResponseWriter, r *http.Request) {
		if g, ok := s.Groups[pathID(r)]; ok {
			// Like Harbor, the single-group response has no ID.
			out := *g
			out.ID = 0
			writeJSON(w, http.StatusOK, out)
			return
		}
		writeErr(w, http.StatusNotFound, "group not found")
	})
	h("PUT /usergroups/{id}", func(w http.ResponseWriter, r *http.Request) {
		g, ok := s.Groups[pathID(r)]
		if !ok {
			writeErr(w, http.StatusNotFound, "group not found")
			return
		}
		var body map[string]string
		if !decode(w, r, &body) {
			return
		}
		g.GroupName = body["group_name"]
		w.WriteHeader(http.StatusOK)
	})
	h("DELETE /usergroups/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if _, ok := s.Groups[id]; !ok {
			writeErr(w, http.StatusNotFound, "group not found")
			return
		}
		delete(s.Groups, id)
		w.WriteHeader(http.StatusOK)
	})

	// Projects
	h("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		name := exactQ(r, "name")
		out := []harbor.Project{}
		for _, p := range s.Projects {
			if name == "" || p.Name == name {
				out = append(out, *p)
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	h("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		var req harbor.ProjectReq
		if !decode(w, r, &req) {
			return
		}
		for _, p := range s.Projects {
			if p.Name == req.ProjectName {
				writeErr(w, http.StatusConflict, "project already exists")
				return
			}
		}
		id := s.id()
		md := map[string]string{"public": "false"}
		for k, v := range req.Metadata {
			md[k] = v
		}
		s.Projects[id] = &harbor.Project{ProjectID: id, Name: req.ProjectName, Metadata: md, CVEAllowlist: req.CVEAllowlist}
		limit := int64(-1)
		if req.StorageLimit != nil {
			limit = *req.StorageLimit
		}
		s.Quotas[id] = &harbor.Quota{ID: s.id(), Hard: map[string]int64{"storage": limit}, Used: map[string]int64{"storage": 0}}
		s.Members[id] = []harbor.Member{{ID: s.id(), EntityName: "admin", EntityType: harbor.EntityTypeUser, EntityID: 1, RoleID: harbor.RoleProjectAdmin}}
		created(w, "/projects", id)
	})
	h("GET /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		if p, ok := s.Projects[pathID(r)]; ok {
			writeJSON(w, http.StatusOK, p)
			return
		}
		writeErr(w, http.StatusNotFound, "project not found")
	})
	h("PUT /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.Projects[pathID(r)]
		if !ok {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		var req harbor.ProjectReq
		if !decode(w, r, &req) {
			return
		}
		for k, v := range req.Metadata {
			p.Metadata[k] = v
		}
		if req.CVEAllowlist != nil {
			p.CVEAllowlist = req.CVEAllowlist
		}
		w.WriteHeader(http.StatusOK)
	})
	h("DELETE /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if _, ok := s.Projects[id]; !ok {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		delete(s.Projects, id)
		delete(s.Quotas, id)
		delete(s.Members, id)
		w.WriteHeader(http.StatusOK)
	})

	// Quotas
	h("GET /quotas", func(w http.ResponseWriter, r *http.Request) {
		pid, _ := strconv.ParseInt(r.URL.Query().Get("reference_id"), 10, 64)
		out := []harbor.Quota{}
		if q, ok := s.Quotas[pid]; ok {
			out = append(out, *q)
		}
		writeJSON(w, http.StatusOK, out)
	})
	h("PUT /quotas/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		var body struct {
			Hard map[string]int64 `json:"hard"`
		}
		if !decode(w, r, &body) {
			return
		}
		for _, q := range s.Quotas {
			if q.ID == id {
				q.Hard = body.Hard
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		writeErr(w, http.StatusNotFound, "quota not found")
	})

	// Members
	h("GET /projects/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		pid := pathID(r)
		if _, ok := s.Projects[pid]; !ok {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		out := append([]harbor.Member{}, s.Members[pid]...)
		writeJSON(w, http.StatusOK, out)
	})
	h("POST /projects/{id}/members", func(w http.ResponseWriter, r *http.Request) {
		pid := pathID(r)
		if _, ok := s.Projects[pid]; !ok {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		var req harbor.MemberReq
		if !decode(w, r, &req) {
			return
		}
		m := harbor.Member{ID: s.id(), RoleID: req.RoleID}
		switch {
		case req.MemberUser != nil:
			u := s.userByName(req.MemberUser.Username)
			if u == nil {
				writeErr(w, http.StatusNotFound, "user not found")
				return
			}
			m.EntityName, m.EntityType, m.EntityID = u.Username, harbor.EntityTypeUser, u.UserID
		case req.MemberGroup != nil:
			g, ok := s.Groups[req.MemberGroup.ID]
			if !ok {
				writeErr(w, http.StatusNotFound, "group not found")
				return
			}
			m.EntityName, m.EntityType, m.EntityID = g.GroupName, harbor.EntityTypeGroup, g.ID
		default:
			writeErr(w, http.StatusBadRequest, "member_user or member_group required")
			return
		}
		for _, e := range s.Members[pid] {
			if e.EntityType == m.EntityType && e.EntityID == m.EntityID {
				writeErr(w, http.StatusConflict, "member already exists")
				return
			}
		}
		s.Members[pid] = append(s.Members[pid], m)
		created(w, fmt.Sprintf("/projects/%d/members", pid), m.ID)
	})
	h("PUT /projects/{id}/members/{mid}", func(w http.ResponseWriter, r *http.Request) {
		pid, mid := pathID(r), pathInt(r, "mid")
		var body map[string]int
		if !decode(w, r, &body) {
			return
		}
		for i := range s.Members[pid] {
			if s.Members[pid][i].ID == mid {
				s.Members[pid][i].RoleID = body["role_id"]
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		writeErr(w, http.StatusNotFound, "member not found")
	})
	h("DELETE /projects/{id}/members/{mid}", func(w http.ResponseWriter, r *http.Request) {
		pid, mid := pathID(r), pathInt(r, "mid")
		ms := s.Members[pid]
		for i := range ms {
			if ms[i].ID == mid {
				s.Members[pid] = append(ms[:i], ms[i+1:]...)
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		writeErr(w, http.StatusNotFound, "member not found")
	})

	// Robots
	h("GET /robots", func(w http.ResponseWriter, r *http.Request) {
		// Like Harbor, list system robots unless the query selects a project.
		level, project := "system", ""
		for _, kv := range strings.Split(r.URL.Query().Get("q"), ",") {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "Level":
				level = v
			case "ProjectID":
				pid, _ := strconv.ParseInt(v, 10, 64)
				if p, ok := s.Projects[pid]; ok {
					project = p.Name
				}
			}
		}
		out := make([]harbor.Robot, 0, len(s.Robots))
		for _, rb := range s.Robots {
			if rb.Level != level {
				continue
			}
			if level == "project" && (len(rb.Permissions) == 0 || rb.Permissions[0].Namespace != project) {
				continue
			}
			out = append(out, *rb)
		}
		writeJSON(w, http.StatusOK, out)
	})
	h("POST /robots", func(w http.ResponseWriter, r *http.Request) {
		var req harbor.RobotCreate
		if !decode(w, r, &req) {
			return
		}
		full := "robot$" + req.Name
		if req.Level == "project" && len(req.Permissions) > 0 {
			full = "robot$" + req.Permissions[0].Namespace + "+" + req.Name
		}
		for _, rb := range s.Robots {
			if rb.Name == full {
				writeErr(w, http.StatusConflict, "robot already exists")
				return
			}
		}
		id := s.id()
		s.Robots[id] = &harbor.Robot{ID: id, Name: full, Description: req.Description, Level: req.Level,
			Duration: req.Duration, Disable: req.Disable, ExpiresAt: -1, Permissions: req.Permissions}
		secret := fmt.Sprintf("Generated%dSecret", id)
		s.RobotSecrets[id] = secret
		writeJSON(w, http.StatusCreated, harbor.RobotCreated{ID: id, Name: full, Secret: secret, ExpiresAt: -1})
	})
	h("GET /robots/{id}", func(w http.ResponseWriter, r *http.Request) {
		if rb, ok := s.Robots[pathID(r)]; ok {
			writeJSON(w, http.StatusOK, rb)
			return
		}
		writeErr(w, http.StatusNotFound, "robot not found")
	})
	h("PUT /robots/{id}", func(w http.ResponseWriter, r *http.Request) {
		rb, ok := s.Robots[pathID(r)]
		if !ok {
			writeErr(w, http.StatusNotFound, "robot not found")
			return
		}
		var req harbor.Robot
		if !decode(w, r, &req) {
			return
		}
		if req.Name != rb.Name || req.Level != rb.Level {
			writeErr(w, http.StatusBadRequest, "name and level cannot be changed")
			return
		}
		rb.Description, rb.Duration, rb.Disable, rb.Permissions = req.Description, req.Duration, req.Disable, req.Permissions
		w.WriteHeader(http.StatusOK)
	})
	h("PATCH /robots/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if _, ok := s.Robots[id]; !ok {
			writeErr(w, http.StatusNotFound, "robot not found")
			return
		}
		var body map[string]string
		if !decode(w, r, &body) {
			return
		}
		secret := body["secret"]
		if secret == "" {
			secret = fmt.Sprintf("Refreshed%dSecret", s.id())
		}
		s.RobotSecrets[id] = secret
		writeJSON(w, http.StatusOK, map[string]string{"secret": secret})
	})
	h("DELETE /robots/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := pathID(r)
		if _, ok := s.Robots[id]; !ok {
			writeErr(w, http.StatusNotFound, "robot not found")
			return
		}
		delete(s.Robots, id)
		delete(s.RobotSecrets, id)
		w.WriteHeader(http.StatusOK)
	})
}

// UserByName returns the user with the given name. Callers must hold Mu.
func (s *Server) UserByName(name string) *harbor.User { return s.userByName(name) }

// ProjectByName returns the project with the given name. Callers must hold Mu.
func (s *Server) ProjectByName(name string) *harbor.Project {
	for _, p := range s.Projects {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func (s *Server) userByName(name string) *harbor.User {
	for _, u := range s.Users {
		if u.Username == name {
			return u
		}
	}
	return nil
}

func exactQ(r *http.Request, key string) string {
	k, v, _ := strings.Cut(r.URL.Query().Get("q"), "=")
	if k != key {
		return ""
	}
	return v
}

func pathID(r *http.Request) int64 { return pathInt(r, "id") }

func pathInt(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func created(w http.ResponseWriter, p string, id int64) {
	w.Header().Set("Location", fmt.Sprintf("%s%s/%d", prefix, p, id))
	w.WriteHeader(http.StatusCreated)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"errors": []map[string]string{{"code": http.StatusText(code), "message": msg}}})
}
