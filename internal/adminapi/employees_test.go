package adminapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCreateAndUpdateEmployee(t *testing.T) {
	env := newAdminEnv(t)
	body := fmt.Sprintf(`{"employee_no":"E002","name":"Bob","email":"bob@example.com","department_id":%d}`, env.departmentID)
	rr := env.do("super_admin", http.MethodPost, "/admin/api/employees", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var created employeeDTO
	decodeData(t, rr, &created)
	if created.EmployeeNo != "E002" || created.Name != "Bob" || created.Status != 1 || created.DepartmentID == nil {
		t.Fatalf("created %+v", created)
	}

	rr = env.do("super_admin", http.MethodGet, "/admin/api/employees?q=Bob", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []employeeDTO `json:"items"`
		Total int           `json:"total"`
	}
	decodeData(t, rr, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("page %+v", page)
	}

	update := fmt.Sprintf(`{"employee_no":"E002","name":"Robert","email":"","department_id":%d,"status":0}`, env.departmentID)
	rr = env.do("super_admin", http.MethodPut, "/admin/api/employees/"+created.ID, update)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body.String())
	}
	var updated employeeDTO
	decodeData(t, rr, &updated)
	if updated.Name != "Robert" || updated.Status != 0 || updated.Email != "" {
		t.Fatalf("updated %+v", updated)
	}
}

func TestEmployeeValidation(t *testing.T) {
	env := newAdminEnv(t)

	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing name", `{"employee_no":"E010"}`},
		{"missing employee_no", `{"name":"No Number"}`},
		{"bad status", `{"employee_no":"E011","name":"Bad","status":5}`},
		{"unknown department", `{"employee_no":"E012","name":"Ghost","department_id":999999}`},
		{"duplicate employee_no", `{"employee_no":"E001","name":"Duplicate"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.do("super_admin", http.MethodPost, "/admin/api/employees", tc.body)
			if rr.Code != http.StatusBadRequest && rr.Code != http.StatusConflict {
				t.Fatalf("got %d %s", rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), `"error":null`) {
				t.Fatalf("expected error envelope, got %s", rr.Body.String())
			}
		})
	}
}

func TestAuditorCannotMutateEmployees(t *testing.T) {
	env := newAdminEnv(t)
	body := `{"employee_no":"E020","name":"Nope"}`
	if rr := env.do("auditor", http.MethodPost, "/admin/api/employees", body); rr.Code != http.StatusForbidden {
		t.Fatalf("auditor create got %d", rr.Code)
	}
	if rr := env.do("auditor", http.MethodGet, "/admin/api/employees", ""); rr.Code != http.StatusOK {
		t.Fatalf("auditor list got %d", rr.Code)
	}

	// Operator is allowed to create.
	rr := env.do("operator", http.MethodPost, "/admin/api/employees", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("operator create got %d %s", rr.Code, rr.Body.String())
	}
}

func TestEmployeeMutationRequiresCSRF(t *testing.T) {
	env := newAdminEnv(t)
	s := env.login("super_admin")
	rr := env.doRaw(http.MethodPost, "/admin/api/employees",
		`{"employee_no":"E030","name":"No CSRF"}`, []*http.Cookie{s.session, s.csrf}, false)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d %s", rr.Code, rr.Body.String())
	}
}
