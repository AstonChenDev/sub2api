package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type HuggingFaceHandler struct {
	service *service.HuggingFaceService
}

func NewHuggingFaceHandler(hf *service.HuggingFaceService) *HuggingFaceHandler {
	return &HuggingFaceHandler{service: hf}
}

type hfPoolRequest struct {
	GroupID                int64    `json:"group_id" binding:"required"`
	Name                   string   `json:"name" binding:"required"`
	BaseURL                string   `json:"base_url"`
	Priority               int      `json:"priority"`
	Weight                 int      `json:"weight"`
	Status                 string   `json:"status"`
	Models                 []string `json:"models"`
	FailureThreshold       int      `json:"failure_threshold"`
	CircuitCooldownSeconds int      `json:"circuit_cooldown_seconds"`
}

func (r hfPoolRequest) input() service.HuggingFacePoolInput {
	return service.HuggingFacePoolInput{
		GroupID: r.GroupID, Name: r.Name, BaseURL: r.BaseURL,
		Priority: r.Priority, Weight: r.Weight, Status: r.Status, Models: r.Models,
		FailureThreshold: r.FailureThreshold, CircuitCooldownSeconds: r.CircuitCooldownSeconds,
	}
}

func parseHFID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param(name)), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid "+name)
		return 0, false
	}
	return id, true
}

func (h *HuggingFaceHandler) CreatePool(c *gin.Context) {
	var req hfPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	pool, err := h.service.CreatePool(c.Request.Context(), req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *HuggingFaceHandler) UpdatePool(c *gin.Context) {
	id, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	var req hfPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	pool, err := h.service.UpdatePool(c.Request.Context(), id, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *HuggingFaceHandler) GetPool(c *gin.Context) {
	id, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	pool, err := h.service.GetPool(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pool)
}

func (h *HuggingFaceHandler) ListPools(c *gin.Context) {
	groupID, err := strconv.ParseInt(strings.TrimSpace(c.Query("group_id")), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "group_id is required")
		return
	}
	pools, err := h.service.ListPools(c.Request.Context(), groupID, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pools)
}

func (h *HuggingFaceHandler) DeletePool(c *gin.Context) {
	id, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	if err := h.service.DeletePool(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Hugging Face pool deleted"})
}

type hfCredentialImportRequest struct {
	Credentials []service.HuggingFaceCredentialImport `json:"credentials" binding:"required,min=1,max=100000"`
}

func (h *HuggingFaceHandler) ImportCredentials(c *gin.Context) {
	poolID, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	var req hfCredentialImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.service.ImportCredentials(c.Request.Context(), poolID, req.Credentials)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *HuggingFaceHandler) ListCredentials(c *gin.Context) {
	poolID, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	page, err := h.service.ListCredentials(c.Request.Context(), poolID, limit, offset)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, page)
}

func (h *HuggingFaceHandler) RecoverCredential(c *gin.Context) {
	poolID, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	accountID, ok := parseHFID(c, "account_id")
	if !ok {
		return
	}
	if err := h.service.RecoverCredential(c.Request.Context(), poolID, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Hugging Face credential recovered"})
}

func (h *HuggingFaceHandler) DeleteCredential(c *gin.Context) {
	poolID, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	accountID, ok := parseHFID(c, "account_id")
	if !ok {
		return
	}
	if err := h.service.DeleteCredential(c.Request.Context(), poolID, accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Hugging Face credential deleted"})
}

func (h *HuggingFaceHandler) ReconcilePool(c *gin.Context) {
	poolID, ok := parseHFID(c, "pool_id")
	if !ok {
		return
	}
	if err := h.service.ReconcilePool(c.Request.Context(), poolID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Hugging Face pool index reconciled"})
}

func (h *HuggingFaceHandler) RecoverDue(c *gin.Context) {
	count, err := h.service.RecoverDue(c.Request.Context(), 10000)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"recovered": count})
}
