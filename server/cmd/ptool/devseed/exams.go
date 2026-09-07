// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (r *runner) exam(index int, unit, class string, teacher *account) error {
	key := fmt.Sprintf("exam/%02d", index+1)
	template := examinationTemplate(index)
	title := template.Title
	if index >= 4 {
		title += fmt.Sprintf(" — Paper %d", index+1)
	}
	instructions := "# " + title + "\n\n" + template.Instructions
	resolveExam := func() (json.RawMessage, bool, error) {
		raw, found, err := r.find("/api/v1/exams?academic_unit_id="+unit, teacher.Token, "title", title)()
		if err != nil || !found {
			return nil, false, err
		}
		raw, err = r.get("/api/v1/exams/"+str(decode(raw), "id"), teacher.Token)
		return raw, true, err
	}
	created, err := r.step(key, teacher.Token, "POST", "/api/v1/exams", object{"academic_unit_id": unit, "title": title, "instructions_markdown": instructions}, nil, resolveExam)
	if err != nil {
		return err
	}
	id := str(nested(created, "exam"), "id")
	path := "/api/v1/exams/" + id
	if err = r.remember(key, id, title, path); err != nil {
		return err
	}
	// One completely bare draft complements the richly populated examples.
	if index%3 != 1 {
		for _, directory := range template.Directories {
			if _, err = r.examChange(key+"/directory/"+directory, teacher.Token, path+"/draft/starter-workspace/directories", path, "POST", object{"path": directory}, nil); err != nil {
				return err
			}
		}
		for _, file := range template.Files {
			body := uploadMetadata([]byte(file.Content), file.Media)
			body["path"] = file.Name
			var value object
			value, err = r.examChange(key+"/file/"+file.Name, teacher.Token, path+"/draft/starter-workspace/files", path, "POST", body, []byte(file.Content))
			if err != nil {
				return err
			}
			entryID := str(value, "id")
			if err = r.remember(key+"/file/"+file.Name, entryID, file.Name, path+"/draft/starter-workspace/files/"+entryID+"/content"); err != nil {
				return err
			}
		}
		for _, resource := range template.Resources {
			body := uploadMetadata([]byte(resource.Content), resource.Media)
			body["display_name"] = resource.Name
			body["description_markdown"] = "Synthetic read-only reference for this examination."
			var value object
			value, err = r.examChange(key+"/resource/"+resource.Name, teacher.Token, path+"/draft/resources", path, "POST", body, []byte(resource.Content))
			if err != nil {
				return err
			}
			resourceID := str(value, "id")
			if err = r.remember(key+"/resource/"+resource.Name, resourceID, resource.Name, path+"/draft/resources/"+resourceID+"/content"); err != nil {
				return err
			}
		}
	}
	_, err = r.examChange(key+"/policy", teacher.Token, path+"/draft/policies/focus-loss", path, "PUT", object{"enabled": index%2 == 0, "minimum_duration_milliseconds": 1500, "incident_count": 3, "window_milliseconds": 60000, "outcome": "flag_and_warn"}, nil)
	if err != nil {
		return err
	}
	if index%3 == 1 {
		return nil
	}
	revision, err := r.examChange(key+"/publication/1", teacher.Token, path+"/revisions", path, "POST", object{}, nil)
	if err != nil {
		return err
	}
	if err = r.remember(key+"/revision/1", str(revision, "id"), title+" — Revision 1", path+"/revisions/"+str(revision, "id")); err != nil {
		return err
	}
	if index%3 == 0 {
		if _, err = r.examChange(key+"/edit", teacher.Token, path+"/draft", path, "PATCH", object{"instructions_markdown": instructions + "\n## Clarification\n\n" + template.Clarification}, nil); err != nil {
			return err
		}
		revision, err = r.examChange(key+"/publication/2", teacher.Token, path+"/revisions", path, "POST", object{}, nil)
		if err != nil {
			return err
		}
		if err = r.remember(key+"/revision/2", str(revision, "id"), title+" — Revision 2", path+"/revisions/"+str(revision, "id")); err != nil {
			return err
		}
	}
	sittingKey := fmt.Sprintf("%s/sitting/%d", key, r.state.Generation)
	start := r.state.ScheduleAt.Add(time.Duration(7+index) * 24 * time.Hour).Truncate(time.Second)
	if op := r.state.Operations[sittingKey]; op == nil && !start.After(time.Now().Add(time.Minute)) {
		return errors.New("saved schedule has elapsed; finish with a fresh isolated dataset or inspect the pending journal")
	}
	sitting, err := r.step(sittingKey, teacher.Token, "POST", path+"/sittings", object{"exam_revision_id": str(revision, "id"), "class_id": class, "scheduled_start_at": start.Format(time.RFC3339), "scheduled_end_at": start.Add(2 * time.Hour).Format(time.RFC3339)}, nil, r.findWhere(path+"/sittings", teacher.Token, func(item object) bool {
		return str(item, "class_id") == class && str(item, "exam_revision_id") == str(revision, "id") && str(item, "scheduled_start_at") == start.Format(time.RFC3339) && str(item, "scheduled_end_at") == start.Add(2*time.Hour).Format(time.RFC3339)
	}))
	if err != nil {
		return err
	}
	sittingID := str(sitting, "id")
	label := title + " — Upcoming Sitting"
	if index%3 == 2 {
		label = title + " — Canceled Sitting"
		if _, err = r.step(sittingKey+"/cancel", teacher.Token, "POST", path+"/sittings/"+sittingID+"/cancel", object{"expected_revision": number(sitting, "revision"), "reason": "Synthetic timetable change: replacement arrangements are pending."}, nil, nil); err != nil {
			return err
		}
	}
	return r.remember(sittingKey, sittingID, label, path+"/sittings/"+sittingID)
}

func uploadMetadata(content []byte, media string) object {
	digest := sha256.Sum256(content)
	return object{"media_type": media, "size": len(content), "sha256": hex.EncodeToString(digest[:])}
}

func (r *runner) examChange(key, token, path, examPath, method string, body object, content []byte) (object, error) {
	if op := r.state.Operations[key]; op != nil {
		return r.step(key, token, method, path, body, content, nil)
	}
	raw, err := r.get(examPath, token)
	if err != nil {
		return nil, err
	}
	var value struct {
		Draft struct {
			Revision int64 `json:"revision"`
		} `json:"draft"`
	}
	if err = json.Unmarshal(raw, &value); err != nil || value.Draft.Revision < 1 {
		return nil, errors.New("invalid exam draft response")
	}
	body["expected_draft_revision"] = value.Draft.Revision
	return r.step(key, token, method, path, body, content, nil)
}
