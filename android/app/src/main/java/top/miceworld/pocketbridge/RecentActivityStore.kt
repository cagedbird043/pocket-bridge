package top.miceworld.pocketbridge

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import kotlin.math.abs

object RecentActivityStore {
    private const val PREFS = "pocket_bridge"
    private const val KEY_RECENT_ACTIVITY_JSON = "recent_activity_json"
    private const val MAX_ENTRIES = 60
    private const val DUPLICATE_WINDOW_MS = 8_000L

    fun load(context: Context): List<RecentActivityEntry> {
        val raw = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .getString(KEY_RECENT_ACTIVITY_JSON, "[]")
            .orEmpty()
        return runCatching {
            val array = JSONArray(raw)
            buildList {
                for (index in 0 until array.length()) {
                    val item = array.optJSONObject(index) ?: continue
                    RecentActivityEntry.fromJson(item)?.let(::add)
                }
            }
        }.getOrElse { emptyList() }
            .sortedByDescending { it.timestampMs }
            .take(MAX_ENTRIES)
    }

    fun append(context: Context, entry: RecentActivityEntry): List<RecentActivityEntry> {
        val current = load(context)
        if (current.any { shouldSuppress(existing = it, candidate = entry) }) {
            return current
        }
        val next = (current + entry)
            .sortedByDescending { it.timestampMs }
            .take(MAX_ENTRIES)
        save(context, next)
        return next
    }

    private fun save(context: Context, entries: List<RecentActivityEntry>) {
        val array = JSONArray()
        entries.forEach { array.put(it.toJson()) }
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit()
            .putString(KEY_RECENT_ACTIVITY_JSON, array.toString())
            .apply()
    }

    private fun shouldSuppress(existing: RecentActivityEntry, candidate: RecentActivityEntry): Boolean {
        if (existing.dedupeKey != candidate.dedupeKey) {
            return false
        }
        return abs(existing.timestampMs - candidate.timestampMs) <= DUPLICATE_WINDOW_MS
    }
}
