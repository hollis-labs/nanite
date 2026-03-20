import { useState } from 'react'
import { HelpCircle, CheckCircle, XCircle, Trophy } from 'lucide-react'

interface TriviaQuestionData {
  question: string
  category: string
  difficulty: string
  answers: string[]
  correct_index: number
  selected_index?: number
  correct?: boolean
  score?: number
  source: string
}

interface TriviaQuestionCardProps {
  data: TriviaQuestionData
  onSendMessage?: (content: string) => void
}

const DIFFICULTY_COLORS: Record<string, string> = {
  easy: 'bg-green-500/20 text-green-400 border-green-500/25',
  medium: 'bg-amber-500/20 text-amber-400 border-amber-500/25',
  hard: 'bg-red-500/20 text-red-400 border-red-500/25',
}

const ANSWER_LABELS = ['A', 'B', 'C', 'D']

export function TriviaQuestionCard({ data, onSendMessage }: TriviaQuestionCardProps) {
  const [submitted, setSubmitted] = useState(data.selected_index !== undefined)
  const hasResult = data.selected_index !== undefined
  const difficultyClass = DIFFICULTY_COLORS[data.difficulty] || DIFFICULTY_COLORS.medium

  const handleAnswer = (index: number) => {
    if (submitted || !onSendMessage) return
    setSubmitted(true)
    onSendMessage(`!answer ${ANSWER_LABELS[index]}`)
  }

  return (
    <div className="animate-in fade-in zoom-in-95 duration-500 ease-out">
      <div className="rounded-lg border border-purple-500/20 bg-zinc-900/50 overflow-hidden max-w-md">
        {/* Header */}
        <div className="flex items-center gap-2 px-4 pt-3 pb-2 border-b border-zinc-800">
          <HelpCircle className="h-4 w-4 text-purple-400 shrink-0" />
          <span className="text-sm font-medium text-zinc-200">Trivia Time!</span>
          <div className="ml-auto flex items-center gap-2">
            <span className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-medium border ${difficultyClass}`}>
              {data.difficulty}
            </span>
            <span className="text-[10px] text-zinc-600 uppercase tracking-wider">
              {data.category}
            </span>
          </div>
        </div>

        {/* Question */}
        <div className="px-4 pt-3 pb-2">
          <p className="text-sm text-zinc-200">{data.question}</p>
        </div>

        {/* Answer buttons */}
        <div className="px-4 pb-3 space-y-1.5">
          {data.answers.map((answer, i) => {
            let btnClass = 'border-zinc-700 bg-zinc-800/50 hover:bg-zinc-700/50 text-zinc-300 hover:text-zinc-100'

            if (hasResult) {
              if (i === data.correct_index) {
                btnClass = 'border-green-500/30 bg-green-500/10 text-green-400'
              } else if (i === data.selected_index) {
                btnClass = 'border-red-500/30 bg-red-500/10 text-red-400'
              } else {
                btnClass = 'border-zinc-800 bg-zinc-900/50 text-zinc-600'
              }
            }

            return (
              <button
                key={`${ANSWER_LABELS[i]}-${answer}`}
                type="button"
                disabled={submitted}
                onClick={() => handleAnswer(i)}
                className={`w-full flex items-center gap-2 rounded-md border px-3 py-2 text-left text-xs transition-colors ${btnClass} ${
                  submitted ? 'cursor-default' : 'cursor-pointer'
                }`}
              >
                <span className="shrink-0 w-5 h-5 rounded-full border border-current flex items-center justify-center text-[10px] font-bold">
                  {ANSWER_LABELS[i]}
                </span>
                <span className="flex-1">{answer}</span>
                {hasResult && i === data.correct_index && (
                  <CheckCircle className="h-3.5 w-3.5 text-green-400 shrink-0" />
                )}
                {hasResult && i === data.selected_index && i !== data.correct_index && (
                  <XCircle className="h-3.5 w-3.5 text-red-400 shrink-0" />
                )}
              </button>
            )
          })}
        </div>

        {/* Result + score */}
        {hasResult && (
          <div className={`px-4 pb-3 pt-2 border-t border-zinc-800`}>
            <div className="flex items-center justify-between">
              <span className={`text-sm font-medium ${data.correct ? 'text-green-400' : 'text-red-400'}`}>
                {data.correct ? 'Correct!' : 'Wrong!'}
              </span>
              {data.score !== undefined && (
                <div className="flex items-center gap-1 text-amber-400 text-xs">
                  <Trophy className="h-3 w-3" />
                  <span>Score: {data.score}</span>
                </div>
              )}
            </div>
          </div>
        )}

        {/* Footer */}
        <div className="px-4 pb-2 flex items-center justify-between">
          <span className="text-[10px] text-zinc-600 uppercase tracking-wider">
            {data.source || 'Open Trivia DB'}
          </span>
          {onSendMessage && !hasResult && !submitted && (
            <span className="text-[10px] text-zinc-600">Click an answer above</span>
          )}
          {onSendMessage && hasResult && (
            <button
              type="button"
              onClick={() => onSendMessage('!trivia')}
              className="text-[11px] text-purple-400 hover:text-purple-300 transition-colors"
            >
              Next question &rarr;
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
