package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListTodos(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.TodoFilter{
		Scope:    q.Get("scope"),
		ScopeID:  q.Get("scope_id"),
		Status:   q.Get("status"),
		Priority: q.Get("priority"),
		ParentID: q.Get("parent_id"),
		Labels:   q.Get("labels"),
	}
	todos, err := a.Services.Todos.ListTodos(r.Context(), f)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, todos)
}

func (a *API) handleCreateTodo(w http.ResponseWriter, r *http.Request) {
	var t store.Todo
	if err := a.decode(r, &t); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := a.Services.Todos.CreateTodo(r.Context(), &t); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusCreated, t)
}

func (a *API) handleGetTodo(w http.ResponseWriter, r *http.Request) {
	t, err := a.Services.Todos.GetTodo(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusNotFound, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, t)
}

func (a *API) handleUpdateTodo(w http.ResponseWriter, r *http.Request) {
	var updates service.TodoUpdates
	if err := a.decode(r, &updates); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	t, err := a.Services.Todos.UpdateTodo(r.Context(), r.PathValue("id"), updates)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusOK, t)
}

func (a *API) handleDeleteTodo(w http.ResponseWriter, r *http.Request) {
	if err := a.Services.Todos.DeleteTodo(r.Context(), r.PathValue("id")); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.Services.Streams.BroadcastWorkChanged()
	a.jsonResp(w, http.StatusOK, map[string]string{"deleted": r.PathValue("id")})
}

func (a *API) handleListTodoChildren(w http.ResponseWriter, r *http.Request) {
	children, err := a.Services.Todos.ListTodoChildren(r.Context(), r.PathValue("id"))
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, children)
}
