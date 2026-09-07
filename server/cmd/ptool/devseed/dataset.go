// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"
)

type datasetSize struct{ Students, Teachers, Exams int }

func sizeFor(profile string) datasetSize {
	switch profile {
	case "small":
		return datasetSize{Students: 8, Teachers: 3, Exams: 4}
	case "large":
		return datasetSize{Students: 600, Teachers: 24, Exams: 36}
	default:
		return datasetSize{Students: 150, Teachers: 10, Exams: 12}
	}
}

type person struct{ First, Last, Timezone string }

func people(seed int64, count int) []person {
	first := []string{"Samira", "Amara", "Lucas", "Mei", "Sofia", "Ibrahim", "Chloé", "Arjun", "Nadia", "Léa", "Daniel", "Zainab", "Oscar", "Yuki", "Kwame", "Fatima", "Elena", "Noah", "Amina", "Mateo"}
	last := []string{"Okafor", "Bennett", "Chen", "Diallo", "Martins", "Khan", "Dubois", "Patel", "Mensah", "Silva", "Nakamura", "Rossi", "Ahmed", "Wilson", "García", "Nguyen", "Osei", "Haddad", "Williams", "Santos"}
	// #nosec G404 G115 -- This PRNG uses the seed's bit pattern only for fictional names; credentials use crypto/rand.
	rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x73656564))
	result := make([]person, count)
	zones := []string{"Europe/London", "Africa/Lagos", "Europe/Paris", "Asia/Kolkata", "UTC"}
	for i := range result {
		result[i] = person{First: first[rng.IntN(len(first))], Last: last[rng.IntN(len(last))], Timezone: zones[i%len(zones)]}
	}
	return result
}

func (r *runner) named(key, path, name, label string, extra object) (string, error) {
	body := object{"name": name, "display_name": label, "description": "Synthetic Northbridge development fixture"}
	for k, value := range extra {
		body[k] = value
	}
	value, err := r.create(key, path, r.state.Accounts["administrator"].Token, body)
	if err != nil {
		return "", err
	}
	id := str(value, "id")
	// Nested collection reads and member reads use different public roots.
	root := path[strings.LastIndex(path, "/")+1:]
	if root == "children" {
		root = "academic-units"
	}
	if root == "levels" {
		root = "programme-levels"
	}
	return id, r.remember(key, id, label, "/api/v1/"+root+"/"+id)
}

func (r *runner) populate() error {
	admin := r.state.Accounts["administrator"]
	shape := sizeFor(r.state.Profile)
	manager, err := r.create("role/manager", "/api/v1/roles", admin.Token, object{"name": "development-exam-manager", "display_name": "Exam Manager", "description": "Synthetic scoped examination author", "permissions": []string{"academic_unit.view", "class.view", "exam.create", "exam.manage", "exam.publish", "exam.sitting.create", "exam.sitting.manage", "exam.sitting.view", "exam.view", "programme.view", "programme_level.view", "submission.release", "submission.review", "submission.view"}})
	if err != nil {
		return err
	}
	reader, err := r.create("role/reader", "/api/v1/roles", admin.Token, object{"name": "development-academic-reader", "display_name": "Academic Directory Reader", "description": "Synthetic limited Academic Unit role", "permissions": []string{"academic_unit.view", "programme.view", "programme_level.view", "class.view"}})
	if err != nil {
		return err
	}
	for key, role := range map[string]object{"role/manager": manager, "role/reader": reader} {
		if err = r.remember(key, str(role, "id"), str(role, "display_name"), "/api/v1/roles/"+str(role, "id")); err != nil {
			return err
		}
	}
	root, err := r.named("unit/science", "/api/v1/academic-units", "science", "Faculty of Science and Technology", nil)
	if err != nil {
		return err
	}
	units := make([]string, 2)
	for i, label := range []string{"Computing and Information Systems", "Mathematics and Statistics"} {
		units[i], err = r.named(fmt.Sprintf("unit/%d", i), "/api/v1/academic-units/"+root+"/children", fmt.Sprintf("school-%d", i+1), label, nil)
		if err != nil {
			return err
		}
	}
	periods := make([]string, 2)
	for i, label := range []string{"Current Academic Period", "Previous Academic Period"} {
		start, end := r.state.StartedAt.AddDate(0, -3, 0), r.state.StartedAt.AddDate(2, 0, 0)
		if i == 1 {
			start, end = r.state.StartedAt.AddDate(-1, -3, 0), r.state.StartedAt.AddDate(0, -4, 0)
		}
		periods[i], err = r.named(fmt.Sprintf("period/%d", i), "/api/v1/academic-periods", fmt.Sprintf("development-period-%d", i), label, object{"owner_type": "institution", "owner_id": r.state.InstitutionID, "start_at": start.UnixMilli(), "end_at": end.UnixMilli()})
		if err != nil {
			return err
		}
	}
	classes := make([]string, 6)
	classUnits := make([]string, 6)
	for i, label := range []string{"BSc Computer Science", "BEng Software Engineering", "BSc Applied Mathematics"} {
		unit := units[i/2]
		var programme string
		programme, err = r.named(fmt.Sprintf("programme/%d", i), "/api/v1/academic-units/"+unit+"/programmes", fmt.Sprintf("programme-%d", i), label, nil)
		if err != nil {
			return err
		}
		for level := 0; level < 2; level++ {
			key := fmt.Sprintf("level/%d/%d", i, level)
			var id string
			id, err = r.named(key, "/api/v1/programmes/"+programme+"/levels", fmt.Sprintf("level-%d", level+1), fmt.Sprintf("Year %d", level+1), nil)
			if err != nil {
				return err
			}
			index := i*2 + level
			period := periods[0]
			if index == 5 {
				period = periods[1]
			}
			classes[index], err = r.named(fmt.Sprintf("class/%d", index), "/api/v1/programme-levels/"+id+"/classes", fmt.Sprintf("development-class-%d", index), fmt.Sprintf("%s — Year %d", label, level+1), object{"academic_period_id": period})
			if err != nil {
				return err
			}
			classUnits[index] = unit
		}
	}
	population := people(r.state.Seed, shape.Students+shape.Teachers)
	teachers := make([]*account, shape.Teachers)
	for i := range teachers {
		key := fmt.Sprintf("teacher-%02d", i+1)
		teachers[i], err = r.invitedAccount(key, population[i], "teacher", units[i%2], str(manager, "id"))
		if err != nil {
			return err
		}
		if err = r.login(teachers[i]); err != nil {
			return err
		}
	}
	for i := 0; i < shape.Students; i++ {
		// Half the candidates share one roster: desktop=75, large=300.
		classIndex := 0
		if i >= shape.Students/2 {
			classIndex = 1 + i%3
		}
		key := fmt.Sprintf("candidate-%03d", i+1)
		var a *account
		a, err = r.invitedAccount(key, population[shape.Teachers+i], "student", classes[classIndex], "")
		if err != nil {
			return err
		}
		if i < 4 {
			if err = r.login(a); err != nil {
				return err
			}
			if err = r.settings(key, a, i); err != nil {
				return err
			}
		}
		if i == 0 {
			// Preserve an actual ended enrollment in a different Academic Period.
			pastStart := r.state.StartedAt.AddDate(-1, -2, 0).UnixMilli()
			historyUser := teachers[0].UserID
			pastPath := "/api/v1/users/" + historyUser + "/affiliations"
			_, err = r.step("affiliation/previous-student", admin.Token, "POST", pastPath, object{"kind": "student", "start_at": pastStart}, nil, r.findWhere(pastPath, admin.Token, func(item object) bool { return str(item, "kind") == "student" && number(item, "start_at") == pastStart }))
			if err != nil {
				return err
			}
			path := "/api/v1/classes/" + classes[5] + "/members"
			_, err = r.step("membership/previous", admin.Token, "POST", path, object{"user_id": historyUser, "start_at": r.state.StartedAt.AddDate(-1, -2, 0).UnixMilli(), "end_at": r.state.StartedAt.AddDate(0, -5, 0).UnixMilli()}, nil, r.find(path+"?history=true", admin.Token, "user_id", historyUser))
			if err != nil {
				return err
			}
			path = "/api/v1/users/" + a.UserID + "/affiliations"
			_, err = r.step("affiliation/student-staff", admin.Token, "POST", path, object{"kind": "staff", "start_at": r.state.StartedAt.UnixMilli()}, nil, r.find(path, admin.Token, "kind", "staff"))
			if err != nil {
				return err
			}
		}
		if (i+1)%25 == 0 {
			fmt.Fprintf(r.out, "Candidates prepared: %d/%d\n", i+1, shape.Students)
		}
	}
	// A teacher with read-only scope provides permission-denied UI coverage.
	limited, err := r.invitedAccount("academic-reader", person{"Rowan", "Ellis", "Europe/London"}, "teacher", units[0], str(reader, "id"))
	if err != nil {
		return err
	}
	if err = r.login(limited); err != nil {
		return err
	}
	if err = r.settings("academic-reader", limited, 3); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("invitation/pending-%d", i)
		email := fmt.Sprintf("prospective-%d@northbridge.example", i+1)
		var value object
		value, err = r.step(key, admin.Token, "POST", "/api/v1/classes/"+classes[4]+"/invitations/student", object{"email": email, "suggested_display_name": fmt.Sprintf("Prospective Student %d", i+1)}, nil, r.find("/api/v1/invitations", admin.Token, "email", email))
		if err != nil {
			return err
		}
		id := str(value, "id")
		if err = r.remember(key, id, "Pending student invitation", "/api/v1/invitations/"+id); err != nil {
			return err
		}
	}
	for i := 0; i < shape.Exams; i++ {
		classIndex := i % 5
		teacher := teachers[0]
		if classUnits[classIndex] == units[1] {
			teacher = teachers[1]
		}
		if err = r.exam(i, classUnits[classIndex], classes[classIndex], teacher); err != nil {
			return err
		}
	}
	if err = r.settings("teacher-01", teachers[0], 1); err != nil {
		return err
	}
	for _, fixture := range []struct{ Alias, Kind, Scope, Role string }{
		{"disabled-student", "student", classes[3], ""},
		{"disabled-teacher", "teacher", units[0], str(reader, "id")},
	} {
		a, createErr := r.invitedAccount(fixture.Alias, person{"Alex", "Rivera", "UTC"}, fixture.Kind, fixture.Scope, fixture.Role)
		if createErr != nil {
			return createErr
		}
		if err = r.disableAccount(fixture.Alias, a); err != nil {
			return err
		}
	}
	for _, alias := range []string{"administrator", "teacher-01", "teacher-02", "candidate-001", "candidate-003", "candidate-005"} {
		if err = r.profilePicture(alias); err != nil {
			return err
		}
	}
	for _, key := range []string{"candidate-001", "academic-reader"} {
		if err = r.enrollMFA(key, r.state.Accounts[key]); err != nil {
			return err
		}
	}
	return nil
}

func (r *runner) invitedAccount(key string, p person, kind, scope, role string) (*account, error) {
	a, err := r.account(key, key, key+"@northbridge.example")
	if err != nil {
		return nil, err
	}
	if a.UserID != "" {
		return a, r.remember("user/"+key, a.UserID, p.First+" "+p.Last, "/api/v1/users/"+a.UserID)
	}
	admin := r.state.Accounts["administrator"].Token
	path, accept := "/api/v1/classes/"+scope+"/invitations/student", "/api/v1/invitations/student-class/accept"
	body := object{"email": a.Email, "suggested_username": a.Username, "suggested_display_name": p.First + " " + p.Last, "suggested_first_name": p.First, "suggested_last_name": p.Last, "suggested_locale": "en", "suggested_timezone": p.Timezone}
	if kind == "teacher" {
		path = "/api/v1/academic-units/" + scope + "/invitations/teacher"
		accept = "/api/v1/invitations/teacher-academic-unit/accept"
		body["role_id"] = role
	}
	invitation, err := r.step("invite/"+key, admin, "POST", path, body, nil, r.find("/api/v1/invitations", admin, "email", a.Email))
	if err != nil {
		return nil, err
	}
	// An accepted Invitation is authoritative when the acceptance response was lost.
	raw, err := r.get("/api/v1/invitations/"+str(invitation, "id"), admin)
	if err != nil {
		return nil, err
	}
	if id := str(decode(raw), "accepted_user_id"); id != "" {
		a.UserID = id
		if err = r.login(a); err != nil {
			return nil, err
		}
	} else {
		claim := ""
		if op := r.state.Operations["accept/"+key]; op != nil {
			claim = str(decode(op.Body), "claim")
		}
		if claim == "" {
			claim, err = r.invitationClaim(a.Email, number(decode(raw), "created_at"))
			if err != nil {
				return nil, err
			}
		}
		var value object
		value, err = r.step("accept/"+key, "", "POST", accept, object{"claim": claim, "username": a.Username, "password": a.Password, "display_name": p.First + " " + p.Last, "first_name": p.First, "last_name": p.Last, "locale": "en", "timezone": p.Timezone}, nil, nil)
		if err != nil {
			return nil, err
		}
		a.UserID = str(value, "user_id")
	}
	if err = r.remember("user/"+key, a.UserID, p.First+" "+p.Last, "/api/v1/users/"+a.UserID); err != nil {
		return nil, err
	}
	return a, nil
}

func (r *runner) settings(key string, a *account, variant int) error {
	if op := r.state.Operations["settings/"+key]; op != nil && op.Done {
		return nil
	}
	raw, err := r.get("/api/v1/users/me/settings", a.Token)
	if err != nil {
		return err
	}
	source := []string{"{}\n", "{\n  // Synthetic preferences for desktop editor development.\n  \"workbench.colorTheme\": \"dark\",\n  \"editor.fontSize\": 16,\n}\n", "{\"editor.tabSize\": 4, \"editor.wordWrap\": \"on\", \"[python]\": {\"editor.tabSize\": 4}}\n", "{\"editor.fontSize\": 20, \"workbench.reduceMotion\": true}\n"}[variant%4]
	_, err = r.step("settings/"+key, a.Token, "PUT", "/api/v1/users/me/settings", object{"expected_revision": str(decode(raw), "revision"), "format_version": 1, "source": source}, nil, func() (json.RawMessage, bool, error) {
		current, readErr := r.get("/api/v1/users/me/settings", a.Token)
		if readErr != nil {
			return nil, false, readErr
		}
		return current, str(decode(current), "source") == source, nil
	})
	return err
}

func (r *runner) invitationClaim(email string, createdAt int64) (string, error) {
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	for {
		raw, _, err := r.request(r.state.Mailpit, "GET", "/api/v1/search?limit=100&query="+url.QueryEscape("to:"+email), "", "", nil, nil)
		if err != nil {
			return "", fmt.Errorf("read local invitation mail: %w", err)
		}
		var list struct {
			Messages []struct {
				ID      string    `json:"ID"`
				Created time.Time `json:"Created"`
			} `json:"messages"`
		}
		if err = json.Unmarshal(raw, &list); err != nil {
			return "", err
		}
		for _, item := range list.Messages {
			if item.Created.Before(time.UnixMilli(createdAt)) {
				continue
			}
			raw, _, err = r.request(r.state.Mailpit, "GET", "/api/v1/message/"+url.PathEscape(item.ID), "", "", nil, nil)
			if err != nil {
				return "", err
			}
			var message struct {
				To         []struct{ Address string }
				Text, HTML string
			}
			if err = json.Unmarshal(raw, &message); err != nil {
				return "", err
			}
			matched := false
			for _, recipient := range message.To {
				if strings.EqualFold(recipient.Address, email) {
					matched = true
				}
			}
			if !matched {
				continue
			}
			for _, text := range []string{message.Text, message.HTML} {
				prefix := r.state.Server + "/join#token="
				_, after, found := strings.Cut(text, prefix)
				if found && len(after) >= 43 {
					claim := after[:43]
					if strings.IndexFunc(claim, func(c rune) bool {
						return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-')
					}) < 0 {
						return claim, nil
					}
				}
			}
		}
		select {
		case <-r.ctx.Done():
			return "", r.ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("invitation mail did not arrive; ensure server Jobs and local Mailpit are running")
		case <-time.After(200 * time.Millisecond):
		}
	}
}
