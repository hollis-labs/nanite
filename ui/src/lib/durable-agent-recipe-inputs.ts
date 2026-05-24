import type {
  DurableAgentRecipe,
  DurableAgentRecipeInput,
  DurableAgentRecipeRequest,
  DurableAgentWakePayload,
} from "./types";

export type RecipeInputValue = string | boolean;
export type RecipeInputValueMap = Record<string, RecipeInputValue>;

const STANDARD_RECIPE_MAPS = new Set([
  "durable_agent.name",
  "durable_agent.slug",
  "durable_agent.profile_id",
  "durable_agent.provider",
  "durable_agent.model",
  "durable_agent.runtime_kind",
  "durable_agent.work_root",
  "wake_payload.prompt",
]);

export function isStandardRecipeInput(input: DurableAgentRecipeInput): boolean {
  return STANDARD_RECIPE_MAPS.has(input.maps_to ?? "");
}

export function initialRecipeInputValues(
  recipe: DurableAgentRecipe | null | undefined,
): RecipeInputValueMap {
  const values: RecipeInputValueMap = {};
  for (const input of recipe?.inputs ?? []) {
    if (input.default === undefined || input.default === null) continue;
    values[input.id] =
      typeof input.default === "boolean"
        ? input.default
        : String(input.default);
  }
  return values;
}

function ensureWakePayload(request: DurableAgentRecipeRequest): DurableAgentWakePayload {
  request.wake_payload = request.wake_payload ?? { reason: "manual" };
  return request.wake_payload;
}

export function applyRecipeInputToRequest(
  request: DurableAgentRecipeRequest,
  input: DurableAgentRecipeInput,
  value: RecipeInputValue | undefined,
): DurableAgentRecipeRequest {
  if (value === undefined) return request;
  const mapsTo = input.maps_to ?? "";
  const text =
    typeof value === "boolean" ? (value ? "true" : "false") : String(value).trim();
  if (text === "") return request;

  switch (mapsTo) {
    case "durable_agent.name":
      request.name = text;
      return request;
    case "durable_agent.slug":
      request.slug = text;
      return request;
    case "durable_agent.profile_id":
      request.profile_id = text;
      return request;
    case "durable_agent.provider":
      request.provider = text;
      return request;
    case "durable_agent.model":
      request.model = text;
      return request;
    case "durable_agent.runtime_kind":
      request.runtime_kind = text;
      return request;
    case "durable_agent.work_root":
      request.work_root = text;
      return request;
    case "wake_payload.prompt":
      ensureWakePayload(request).prompt = text;
      return request;
    case "wake_payload.reason":
      ensureWakePayload(request).reason = text;
      return request;
    default:
      if (mapsTo.startsWith("metadata.")) {
        request.metadata = request.metadata ?? {};
        request.metadata[mapsTo.slice("metadata.".length)] = text;
        return request;
      }
      if (mapsTo.startsWith("wake_payload.facts.")) {
        const wakePayload = ensureWakePayload(request);
        wakePayload.facts = wakePayload.facts ?? {};
        wakePayload.facts[mapsTo.slice("wake_payload.facts.".length)] = text;
        return request;
      }
      if (mapsTo.startsWith("wake_payload.metadata.")) {
        const wakePayload = ensureWakePayload(request);
        wakePayload.metadata = wakePayload.metadata ?? {};
        wakePayload.metadata[mapsTo.slice("wake_payload.metadata.".length)] = text;
        return request;
      }
      return request;
  }
}
