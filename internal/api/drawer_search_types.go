package api

import "github.com/hollis-labs/nanite/internal/store"

// DrawerCardView is a pinned bottom-drawer card as the API returns it. Field
// order is the store row's.
type DrawerCardView struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	CardType   string `json:"card_type"`
	ContentRef string `json:"content_ref"`
	Title      string `json:"title"`
	Payload    string `json:"payload"`
	Position   int    `json:"position"`
	CreatedAt  string `json:"created_at"`
}

func drawerCardToView(c *store.BottomDrawerPinnedCard) DrawerCardView {
	return DrawerCardView{
		ID:         c.ID,
		SessionID:  c.SessionID,
		CardType:   c.CardType,
		ContentRef: c.ContentRef,
		Title:      c.Title,
		Payload:    c.Payload,
		Position:   c.Position,
		CreatedAt:  c.CreatedAt,
	}
}

// drawerCardsToView keeps nil as nil and an empty list as [].
func drawerCardsToView(cards []store.BottomDrawerPinnedCard) []DrawerCardView {
	if cards == nil {
		return nil
	}
	out := make([]DrawerCardView, len(cards))
	for i := range cards {
		out[i] = drawerCardToView(&cards[i])
	}
	return out
}

// SearchResultView is a message search hit as the API returns it. Field
// order is the store row's.
type SearchResultView struct {
	SessionID        string `json:"session_id"`
	MessageID        string `json:"message_id"`
	Role             string `json:"role"`
	Snippet          string `json:"snippet"`
	CreatedAt        string `json:"created_at"`
	SessionTitle     string `json:"session_title"`
	SessionShortCode string `json:"session_short_code"`
}

func searchResultToView(r *store.SearchResult) SearchResultView {
	return SearchResultView{
		SessionID:        r.SessionID,
		MessageID:        r.MessageID,
		Role:             r.Role,
		Snippet:          r.Snippet,
		CreatedAt:        r.CreatedAt,
		SessionTitle:     r.SessionTitle,
		SessionShortCode: r.SessionShortCode,
	}
}

// searchResultsToView keeps nil as nil and an empty list as [].
func searchResultsToView(results []store.SearchResult) []SearchResultView {
	if results == nil {
		return nil
	}
	out := make([]SearchResultView, len(results))
	for i := range results {
		out[i] = searchResultToView(&results[i])
	}
	return out
}
