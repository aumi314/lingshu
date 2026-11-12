package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/aumi314/lingshu/internal/store"
)

// 项目额度（docs/decisions.md「项目额度」）：成员侧在「我的项目」页用这些接口，管理员另有列表和冻结。

// projectsOn 检查项目额度组件开着，没开就回 403。
func (s *Server) projectsOn(w http.ResponseWriter, r *http.Request) bool {
	limits, err := s.Store.CallLimits(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if !limits.ProjectsEnabled {
		writeError(w, http.StatusForbidden, "项目功能没有开启，请联系管理员")
		return false
	}
	return true
}

// projectAs 取项目并检查当前用户的身份：owner 只允许创建人，member 允许创建人和正式成员。
func (s *Server) projectAs(w http.ResponseWriter, r *http.Request, need string) (*store.Project, bool) {
	if !s.projectsOn(w, r) {
		return nil, false
	}
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	status, err := s.Store.ProjectMemberStatus(r.Context(), id, currentUser(r).ID)
	if err != nil {
		writeStoreError(w, err)
		return nil, false
	}
	if status == "" || status == "pending" || (need == "owner" && status != "owner") {
		writeError(w, http.StatusNotFound, "项目不存在，或者你没有权限管理它")
		return nil, false
	}
	p, err := s.Store.ProjectByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return nil, false
	}
	if p.Closed {
		writeError(w, http.StatusBadRequest, "项目已解散")
		return nil, false
	}
	p.MyStatus = status
	if status != "owner" {
		p.InviteCode = ""
	}
	return p, true
}

func writeProjectError(w http.ResponseWriter, err error) {
	var bad *store.InputError
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, bad.Msg)
	case errors.Is(err, store.ErrProjectBusy):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeStoreError(w, err)
	}
}

// myProjects 返回组件是否开着、我能不能建项目，以及我创建、加入、申请中的项目。
func (s *Server) myProjects(w http.ResponseWriter, r *http.Request) {
	limits, err := s.Store.CallLimits(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	u := currentUser(r)
	out := map[string]any{"enabled": limits.ProjectsEnabled, "can_create": u.CanCreateProjects, "projects": []*store.Project{}}
	if limits.ProjectsEnabled {
		projects, err := s.Store.MyProjects(r.Context(), u.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if projects != nil {
			out["projects"] = projects
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !s.projectsOn(w, r) {
		return
	}
	u := currentUser(r)
	if !u.CanCreateProjects {
		writeError(w, http.StatusForbidden, "你还没有创建项目的权限，请联系管理员开通")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" || len([]rune(in.Name)) > 50 {
		writeError(w, http.StatusBadRequest, "项目名称不能为空，最多 50 个字")
		return
	}
	id, err := s.Store.CreateProject(r.Context(), u.ID, in.Name)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	p, err := s.Store.ProjectByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	p.MyStatus = "owner"
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) joinProject(w http.ResponseWriter, r *http.Request) {
	if !s.projectsOn(w, r) {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name, err := s.Store.JoinProject(r.Context(), currentUser(r).ID, in.Code)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// getProject 返回项目和成员；只有创建人能看到每个成员的用量和申请列表，普通成员只看项目本身。
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	p, found := s.projectAs(w, r, "member")
	if !found {
		return
	}
	out := map[string]any{"project": p}
	if p.MyStatus == "owner" {
		members, err := s.Store.ProjectMembers(r.Context(), p.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		out["members"] = members
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	p, found := s.projectAs(w, r, "owner")
	if !found {
		return
	}
	var in struct {
		Name        *string          `json:"name"`
		PeriodLimit *decimal.Decimal `json:"period_limit"`
		NoLimit     bool             `json:"no_period_limit"` // true 表示去掉额度上限
		LimitPeriod *string          `json:"limit_period"`    // day / week / month
		GroupIDs    *[]int64         `json:"group_ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var patch store.ProjectPatch
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len([]rune(name)) > 50 {
			writeError(w, http.StatusBadRequest, "项目名称不能为空，最多 50 个字")
			return
		}
		patch.Name = &name
	}
	switch {
	case in.NoLimit:
		patch.SetPeriodLimit = true
	case in.PeriodLimit != nil:
		if !in.PeriodLimit.IsPositive() {
			writeError(w, http.StatusBadRequest, "额度上限要大于 0；不想限制就去掉上限")
			return
		}
		limit := in.PeriodLimit.Round(10)
		patch.SetPeriodLimit, patch.PeriodLimit = true, &limit
	}
	if in.LimitPeriod != nil {
		if !store.LimitPeriods[*in.LimitPeriod] {
			writeError(w, http.StatusBadRequest, "额度周期只能是按天、按周或按月")
			return
		}
		patch.LimitPeriod = in.LimitPeriod
	}
	patch.GroupIDs = in.GroupIDs
	if err := s.Store.UpdateProject(r.Context(), currentUser(r).ID, p.ID, patch); err != nil {
		writeProjectError(w, err)
		return
	}
	ok(w)
}

func (s *Server) resetProjectCode(w http.ResponseWriter, r *http.Request) {
	p, found := s.projectAs(w, r, "owner")
	if !found {
		return
	}
	code, err := s.Store.ResetInviteCode(r.Context(), currentUser(r).ID, p.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"invite_code": code})
}

func memberID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "成员 id 不对")
		return 0, false
	}
	return id, true
}

func (s *Server) approveProjectMember(w http.ResponseWriter, r *http.Request) {
	p, found := s.projectAs(w, r, "owner")
	if !found {
		return
	}
	uid, okID := memberID(w, r)
	if !okID {
		return
	}
	if err := s.Store.ApproveMember(r.Context(), currentUser(r).ID, p.ID, uid); err != nil {
		writeProjectError(w, err)
		return
	}
	ok(w)
}

// removeProjectMember 创建人拒绝申请或移出成员；成员也可以自己退出（uid 是自己）。
func (s *Server) removeProjectMember(w http.ResponseWriter, r *http.Request) {
	uid, okID := memberID(w, r)
	if !okID {
		return
	}
	need := "owner"
	if uid == currentUser(r).ID {
		need = "member"
	}
	p, found := s.projectAs(w, r, need)
	if !found {
		return
	}
	if uid == p.OwnerID {
		writeError(w, http.StatusBadRequest, "创建人不能退出项目；不再使用可以解散项目")
		return
	}
	if err := s.Store.RemoveMember(r.Context(), currentUser(r).ID, p.ID, uid); err != nil {
		writeProjectError(w, err)
		return
	}
	ok(w)
}

// transferProject 创建人转入（direction=in）或取回（out）项目额度，金额是美元。
func (s *Server) transferProject(w http.ResponseWriter, r *http.Request) {
	p, found := s.projectAs(w, r, "owner")
	if !found {
		return
	}
	var in struct {
		Direction string          `json:"direction"`
		Amount    decimal.Decimal `json:"amount"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	amount := in.Amount.Round(10)
	if !amount.IsPositive() {
		writeError(w, http.StatusBadRequest, "金额要大于 0")
		return
	}
	switch in.Direction {
	case "in":
	case "out":
		amount = amount.Neg()
	default:
		writeError(w, http.StatusBadRequest, "方向只能是转入（in）或取回（out）")
		return
	}
	if err := s.Store.TransferProject(r.Context(), currentUser(r).ID, p.ID, amount); err != nil {
		writeProjectError(w, err)
		return
	}
	ok(w)
}

func (s *Server) closeProject(w http.ResponseWriter, r *http.Request) {
	p, found := s.projectAs(w, r, "owner")
	if !found {
		return
	}
	if err := s.Store.CloseProject(r.Context(), currentUser(r).ID, p.ID); err != nil {
		writeProjectError(w, err)
		return
	}
	ok(w)
}

// projectKeyAllowed 检查用户能不能在这个项目下建 key、用这个分组：组件开着、是正式成员、项目没冻结、分组在项目开放列表里。
func (s *Server) projectKeyAllowed(r *http.Request, projectID, groupID int64) string {
	limits, err := s.Store.CallLimits(r.Context())
	if err != nil {
		return "读取设置失败，请稍后重试"
	}
	if !limits.ProjectsEnabled {
		return "项目功能没有开启，请联系管理员"
	}
	status, err := s.Store.ProjectMemberStatus(r.Context(), projectID, currentUser(r).ID)
	if err != nil || status == "" || status == "pending" {
		return "你还不是这个项目的成员"
	}
	p, err := s.Store.ProjectByID(r.Context(), projectID)
	if err != nil {
		return "项目不存在"
	}
	switch {
	case p.Closed:
		return "项目已解散"
	case p.Frozen:
		return "项目已被管理员冻结"
	case !slices.Contains(p.GroupIDs, groupID):
		return "项目没有开放这个分组，请联系项目负责人"
	}
	return ""
}

// ---- 管理员 ----

func (s *Server) adminProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.Store.AllProjects(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if projects == nil {
		projects = []*store.Project{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) adminFreezeProject(w http.ResponseWriter, r *http.Request) {
	id, okID := pathID(w, r)
	if !okID {
		return
	}
	var in struct {
		Frozen bool `json:"frozen"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.Store.SetProjectFrozen(r.Context(), currentUser(r).ID, id, in.Frozen); err != nil {
		writeStoreError(w, err)
		return
	}
	ok(w)
}
