interface TicketMarkerData {
  id: string
  title: string
  description: string
  category: string
  priority: string
  routing: string
}

/**
 * Build the TICKET_DATA marker that the engine parses to inject a
 * ticket-confirmation envelope. Used by both TicketFormCard and
 * TicketInitFlow — keep in sync here, not in two places.
 */
export function buildTicketDataMarker(data: TicketMarkerData): string {
  return `\n\n<!--TICKET_DATA:${JSON.stringify({
    ...data,
    status: 'open',
    created_at: new Date().toISOString(),
  })}:TICKET_DATA-->`
}

export function buildTicketMessage(
  tid: string,
  title: string,
  category: string,
  priority: string,
  routing: string,
): string {
  return `Ticket created: ${tid} — ${title} [Category: ${category}, Priority: ${priority}, Routing: ${routing}]`
}
