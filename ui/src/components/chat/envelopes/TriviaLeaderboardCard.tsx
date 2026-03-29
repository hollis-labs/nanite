import { Trophy, Medal, Award } from 'lucide-react'

interface ScoreEntry {
  user_id: string
  score: number
  rank: number
}

interface TriviaLeaderboardData {
  entries: ScoreEntry[]
}

interface TriviaLeaderboardCardProps {
  data: TriviaLeaderboardData
  onSendMessage?: (content: string) => void
}

function RankIcon({ rank }: { rank: number }) {
  if (rank === 1) return <Trophy className="h-4 w-4 text-amber-400" />
  if (rank === 2) return <Medal className="h-4 w-4 text-fg-secondary" />
  if (rank === 3) return <Award className="h-4 w-4 text-amber-600" />
  return <span className="w-4 text-center text-xs text-fg-muted">{rank}</span>
}

export function TriviaLeaderboardCard({ data, onSendMessage }: TriviaLeaderboardCardProps) {
  const entries = data.entries || []

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-amber-500/20 bg-bg-elevated/50 overflow-hidden max-w-sm">
        {/* Header */}
        <div className="flex items-center gap-2 px-4 pt-3 pb-2 border-b border-border">
          <Trophy className="h-4 w-4 text-amber-400 shrink-0" />
          <span className="text-sm font-semibold text-fg">Trivia Leaderboard</span>
        </div>

        {entries.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-fg-muted">
            No scores yet. Type <code className="text-accent">!trivia</code> to start playing!
          </div>
        ) : (
          <div className="divide-y divide-border">
            {entries.map((entry) => (
              <div key={entry.user_id} className="flex items-center gap-3 px-4 py-2.5">
                <RankIcon rank={entry.rank} />
                <span className="flex-1 text-sm text-fg-secondary truncate">
                  {entry.user_id}
                </span>
                <span className="text-sm font-mono font-medium text-amber-400">
                  {entry.score}
                </span>
              </div>
            ))}
          </div>
        )}

        {/* Footer actions */}
        <div className="px-4 py-2 border-t border-border flex items-center justify-between">
          <span className="text-[10px] text-fg-faint uppercase tracking-wider">Trivia Game</span>
          {onSendMessage && (
            <button
              type="button"
              onClick={() => onSendMessage('!trivia')}
              className="text-[11px] text-accent hover:text-accent-hover transition-colors"
            >
              Play a round &rarr;
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
