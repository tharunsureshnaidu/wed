package handler

import (
	"net/http"
	"strings"

	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/httpx"
	"github.com/tharunsureshnaidu/wed/marriage-hall-booking/pkg/response"
)

// FAQ limits. Generous enough for a real answer about catering or parking,
// bounded so one venue cannot store an essay per question.
const (
	maxFaqQuestion = 500
	maxFaqAnswer   = 4000
)

type faqReq struct {
	Question  *string `json:"question"`
	Answer    *string `json:"answer"`
	SortOrder *int    `json:"sortOrder"`
}

// listFaqs is GET /api/v1/facilities/{id}/faqs - public, like the rest of the
// facility reads. Also embedded in the detail response, so a client that has
// already fetched the venue does not need this call.
func (h *Handler) listFaqs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !httpx.ValidUUID(id) {
		response.Error(w, http.StatusBadRequest, "Invalid facility id", "VALIDATION_ERROR")
		return
	}
	out, err := h.repo.FaqsOf(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "FAQs retrieved successfully", out)
}

// addFaq is POST /api/v1/facilities/{id}/faqs.
func (h *Handler) addFaq(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	var req faqReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	q, a := trimOr(req.Question), trimOr(req.Answer)
	if msg, ok := validFaq(q, a); !ok {
		response.Error(w, http.StatusBadRequest, msg, "VALIDATION_ERROR")
		return
	}
	sort := 0
	if req.SortOrder != nil {
		sort = *req.SortOrder
	}
	newID, err := h.repo.AddFaq(r.Context(), id, q, a, sort)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.Created(w, "FAQ added successfully",
		"/api/v1/facilities/"+id+"/faqs/"+newID,
		map[string]any{"id": newID, "question": q, "answer": a, "sortOrder": sort})
}

// updateFaq is PUT /api/v1/facilities/{id}/faqs/{childId}. Partial: an omitted
// field is left alone, so fixing a typo in an answer cannot blank the question.
func (h *Handler) updateFaq(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	childID := r.PathValue("childId")
	if !httpx.ValidUUID(childID) {
		response.Error(w, http.StatusBadRequest, "Invalid FAQ id", "VALIDATION_ERROR")
		return
	}
	var req faqReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Question == nil && req.Answer == nil && req.SortOrder == nil {
		response.Error(w, http.StatusBadRequest, "No fields to update", "VALIDATION_ERROR")
		return
	}
	// Only what was sent is validated; a nil means "unchanged", but an
	// explicitly blank question is a real mistake and is rejected.
	var q, a *string
	if req.Question != nil {
		v := strings.TrimSpace(*req.Question)
		if msg, ok := validFaqQuestion(v); !ok {
			response.Error(w, http.StatusBadRequest, msg, "VALIDATION_ERROR")
			return
		}
		q = &v
	}
	if req.Answer != nil {
		v := strings.TrimSpace(*req.Answer)
		if msg, ok := validFaqAnswer(v); !ok {
			response.Error(w, http.StatusBadRequest, msg, "VALIDATION_ERROR")
			return
		}
		a = &v
	}

	found, err := h.repo.UpdateFaq(r.Context(), id, childID, q, a, req.SortOrder)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !found {
		response.Error(w, http.StatusNotFound, "FAQ not found", "FAQ_NOT_FOUND")
		return
	}
	out, err := h.repo.FaqsOf(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	response.OK(w, "FAQ updated successfully", out)
}

// deleteFaq is DELETE /api/v1/facilities/{id}/faqs/{childId}.
func (h *Handler) deleteFaq(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireOwner(w, r)
	if !ok {
		return
	}
	childID := r.PathValue("childId")
	if !httpx.ValidUUID(childID) {
		response.Error(w, http.StatusBadRequest, "Invalid FAQ id", "VALIDATION_ERROR")
		return
	}
	found, err := h.repo.DeleteFaq(r.Context(), id, childID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if !found {
		response.Error(w, http.StatusNotFound, "FAQ not found", "FAQ_NOT_FOUND")
		return
	}
	response.OK(w, "FAQ deleted successfully", nil)
}

func trimOr(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}

func validFaq(question, answer string) (string, bool) {
	if msg, ok := validFaqQuestion(question); !ok {
		return msg, false
	}
	return validFaqAnswer(answer)
}

func validFaqQuestion(v string) (string, bool) {
	if v == "" {
		return "A question is required", false
	}
	if len([]rune(v)) > maxFaqQuestion {
		return "Question must be 500 characters or fewer", false
	}
	return "", true
}

func validFaqAnswer(v string) (string, bool) {
	if v == "" {
		return "An answer is required", false
	}
	if len([]rune(v)) > maxFaqAnswer {
		return "Answer must be 4000 characters or fewer", false
	}
	return "", true
}
