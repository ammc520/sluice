package adminapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type departmentListDTO struct {
	Items           []departmentDTO `json:"items"`
	UnassignedCount int64           `json:"unassigned_count"`
	TotalEmployees  int64           `json:"total_employees"`
}

func deleteDepartmentResult(t *testing.T, rr *httptest.ResponseRecorder) (int64, bool) {
	t.Helper()
	var result struct {
		Deleted        bool  `json:"deleted"`
		MovedEmployees int64 `json:"moved_employees"`
	}
	decodeData(t, rr, &result)
	return result.MovedEmployees, result.Deleted
}

func TestDepartmentCRUD(t *testing.T) {
	env := newAdminEnv(t)

	rr := env.do("super_admin", http.MethodPost, "/admin/api/departments", `{"name":"  Sales  "}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create department: %d %s", rr.Code, rr.Body.String())
	}
	var created departmentDTO
	decodeData(t, rr, &created)
	if created.ID <= 0 || created.Name != "Sales" || created.EmployeeCount != 0 {
		t.Fatalf("created department %+v", created)
	}

	duplicate := env.do("operator", http.MethodPost, "/admin/api/departments", `{"name":"Sales"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate name got %d: %s", duplicate.Code, duplicate.Body.String())
	}

	for _, body := range []string{
		`{"name":""}`,
		`{"name":"   "}`,
		fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", 51)),
		`not-json`,
	} {
		bad := env.do("operator", http.MethodPost, "/admin/api/departments", body)
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("invalid payload %q got %d: %s", body, bad.Code, bad.Body.String())
		}
	}

	path := fmt.Sprintf("/admin/api/departments/%d", created.ID)
	renamed := env.do("operator", http.MethodPut, path, `{"name":"Sales & BD"}`)
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body.String())
	}
	var renamedDTO departmentDTO
	decodeData(t, renamed, &renamedDTO)
	if renamedDTO.Name != "Sales & BD" || renamedDTO.ID != created.ID {
		t.Fatalf("renamed department %+v", renamedDTO)
	}

	taken := env.do("operator", http.MethodPut, path, `{"name":"Engineering"}`)
	if taken.Code != http.StatusConflict {
		t.Fatalf("rename to existing name got %d: %s", taken.Code, taken.Body.String())
	}

	missing := env.do("operator", http.MethodPut, "/admin/api/departments/999999", `{"name":"Ghost"}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("rename missing got %d: %s", missing.Code, missing.Body.String())
	}

	auditor := env.do("auditor", http.MethodPost, "/admin/api/departments", `{"name":"Audit"}`)
	if auditor.Code != http.StatusForbidden {
		t.Fatalf("auditor create got %d: %s", auditor.Code, auditor.Body.String())
	}

	deleted := env.do("operator", http.MethodDelete, path, "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete empty department: %d %s", deleted.Code, deleted.Body.String())
	}
	if moved, ok := deleteDepartmentResult(t, deleted); moved != 0 || !ok {
		t.Fatalf("delete result moved=%d ok=%v", moved, ok)
	}

	gone := env.do("operator", http.MethodDelete, path, "")
	if gone.Code != http.StatusNotFound {
		t.Fatalf("delete missing got %d: %s", gone.Code, gone.Body.String())
	}
}

func TestDepartmentDeleteReassignsEmployees(t *testing.T) {
	env := newAdminEnv(t)

	listURL := "/admin/api/departments"
	rr := env.do("super_admin", http.MethodGet, listURL, "")
	var before departmentListDTO
	decodeData(t, rr, &before)
	if len(before.Items) != 1 || before.Items[0].EmployeeCount != 1 || before.TotalEmployees != 1 {
		t.Fatalf("directory before %+v", before)
	}

	sales := env.do("super_admin", http.MethodPost, listURL, `{"name":"Sales"}`)
	if sales.Code != http.StatusCreated {
		t.Fatalf("create Sales: %d %s", sales.Code, sales.Body.String())
	}
	var salesDTO departmentDTO
	decodeData(t, sales, &salesDTO)

	engineeringPath := fmt.Sprintf("/admin/api/departments/%d", env.departmentID)
	blocked := env.do("operator", http.MethodDelete, engineeringPath, "")
	if blocked.Code != http.StatusConflict {
		t.Fatalf("delete non-empty got %d: %s", blocked.Code, blocked.Body.String())
	}

	selfMove := env.do("operator", http.MethodDelete,
		fmt.Sprintf("%s?move_to=%d", engineeringPath, env.departmentID), "")
	if selfMove.Code != http.StatusBadRequest {
		t.Fatalf("move to self got %d: %s", selfMove.Code, selfMove.Body.String())
	}

	badTarget := env.do("operator", http.MethodDelete, engineeringPath+"?move_to=999999", "")
	if badTarget.Code != http.StatusBadRequest {
		t.Fatalf("move to missing got %d: %s", badTarget.Code, badTarget.Body.String())
	}

	badParam := env.do("operator", http.MethodDelete, engineeringPath+"?move_to=abc", "")
	if badParam.Code != http.StatusBadRequest {
		t.Fatalf("move_to=abc got %d: %s", badParam.Code, badParam.Body.String())
	}

	moved := env.do("operator", http.MethodDelete,
		fmt.Sprintf("%s?move_to=%d", engineeringPath, salesDTO.ID), "")
	if moved.Code != http.StatusOK {
		t.Fatalf("move and delete: %d %s", moved.Code, moved.Body.String())
	}
	if count, ok := deleteDepartmentResult(t, moved); count != 1 || !ok {
		t.Fatalf("move and delete result moved=%d ok=%v", count, ok)
	}

	employees := env.do("auditor", http.MethodGet,
		fmt.Sprintf("/admin/api/employees?department_id=%d", salesDTO.ID), "")
	if employees.Code != http.StatusOK {
		t.Fatalf("list sales employees: %d %s", employees.Code, employees.Body.String())
	}
	var page struct {
		Items []employeeDTO `json:"items"`
		Total int           `json:"total"`
	}
	decodeData(t, employees, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("sales employees %+v", page)
	}
	if page.Items[0].DepartmentName != "Sales" || page.Items[0].DepartmentID == nil || *page.Items[0].DepartmentID != salesDTO.ID {
		t.Fatalf("employee department %+v", page.Items[0])
	}

	after := env.do("auditor", http.MethodGet, listURL, "")
	var directory departmentListDTO
	decodeData(t, after, &directory)
	if len(directory.Items) != 1 || directory.Items[0].Name != "Sales" || directory.Items[0].EmployeeCount != 1 {
		t.Fatalf("directory after move %+v", directory)
	}
	if directory.UnassignedCount != 0 || directory.TotalEmployees != 1 {
		t.Fatalf("totals after move %+v", directory)
	}

	// Reassigning to "none" leaves employees unassigned and removes the department.
	unassigned := env.do("operator", http.MethodDelete,
		fmt.Sprintf("/admin/api/departments/%d?move_to=none", salesDTO.ID), "")
	if unassigned.Code != http.StatusOK {
		t.Fatalf("move to none: %d %s", unassigned.Code, unassigned.Body.String())
	}
	if count, ok := deleteDepartmentResult(t, unassigned); count != 1 || !ok {
		t.Fatalf("move to none result moved=%d ok=%v", count, ok)
	}

	unassignedList := env.do("auditor", http.MethodGet, "/admin/api/employees?unassigned=1", "")
	if unassignedList.Code != http.StatusOK {
		t.Fatalf("unassigned list: %d %s", unassignedList.Code, unassignedList.Body.String())
	}
	decodeData(t, unassignedList, &page)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].DepartmentID != nil {
		t.Fatalf("unassigned employees %+v", page)
	}

	final := env.do("auditor", http.MethodGet, listURL, "")
	decodeData(t, final, &directory)
	if directory.UnassignedCount != 1 || directory.TotalEmployees != 1 {
		t.Fatalf("directory after unassign %+v", directory)
	}
}

func TestEmployeeListFilters(t *testing.T) {
	env := newAdminEnv(t)

	salesID := env.departmentID
	createEmployeeForTest(t, env, "E002", "Bob", salesID)
	unassigned := env.do("super_admin", http.MethodPost, "/admin/api/employees",
		`{"employee_no":"E003","name":"Carol","email":"","department_id":null,"status":0}`)
	if unassigned.Code != http.StatusCreated {
		t.Fatalf("create unassigned employee: %d %s", unassigned.Code, unassigned.Body.String())
	}

	cases := []struct {
		query string
		total int
		names []string
	}{
		{"department_id=" + fmt.Sprint(salesID), 2, []string{"Bob", "Alice"}},
		{"unassigned=1", 1, []string{"Carol"}},
		{"status=0", 1, []string{"Carol"}},
		{"status=1", 2, []string{"Bob", "Alice"}},
	}
	for _, tc := range cases {
		rr := env.do("auditor", http.MethodGet, "/admin/api/employees?"+tc.query, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("%s got %d: %s", tc.query, rr.Code, rr.Body.String())
		}
		var page struct {
			Items []employeeDTO `json:"items"`
			Total int           `json:"total"`
		}
		decodeData(t, rr, &page)
		if page.Total != tc.total || len(page.Items) != tc.total {
			t.Fatalf("%s total=%d items=%+v", tc.query, page.Total, page.Items)
		}
		for index, name := range tc.names {
			if page.Items[index].Name != name {
				t.Fatalf("%s item %d = %q, want %q", tc.query, index, page.Items[index].Name, name)
			}
		}
	}

	for _, query := range []string{
		"unassigned=yes",
		"unassigned=1&department_id=" + fmt.Sprint(salesID),
		"status=2",
		"department_id=0",
	} {
		rr := env.do("auditor", http.MethodGet, "/admin/api/employees?"+query, "")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s got %d: %s", query, rr.Code, rr.Body.String())
		}
	}

	// The listing carries department names so the console never renders ids.
	rr := env.do("auditor", http.MethodGet, "/admin/api/employees?department_id="+fmt.Sprint(salesID), "")
	var page struct {
		Items []employeeDTO `json:"items"`
	}
	decodeData(t, rr, &page)
	for _, item := range page.Items {
		if item.DepartmentName != "Engineering" {
			t.Fatalf("department name %+v", item)
		}
	}
}
