package top.miceworld.pocketbridge

import org.json.JSONObject

data class RecentActivityEntry(
    val kind: String,
    val title: String,
    val body: String,
    val fromDeviceId: String,
    val ingress: String,
    val timestampMs: Long,
    val dedupeKey: String,
) {
    fun toJson(): JSONObject = JSONObject()
        .put(KEY_KIND, kind)
        .put(KEY_TITLE, title)
        .put(KEY_BODY, body)
        .put(KEY_FROM_DEVICE_ID, fromDeviceId)
        .put(KEY_INGRESS, ingress)
        .put(KEY_TIMESTAMP_MS, timestampMs)
        .put(KEY_DEDUPE_KEY, dedupeKey)

    companion object {
        const val KIND_NOTIFY = "notify"
        const val KIND_TASK = "task"

        const val INGRESS_WEBSOCKET = "websocket"
        const val INGRESS_FCM = "fcm"

        private const val KEY_KIND = "kind"
        private const val KEY_TITLE = "title"
        private const val KEY_BODY = "body"
        private const val KEY_FROM_DEVICE_ID = "from_device_id"
        private const val KEY_INGRESS = "ingress"
        private const val KEY_TIMESTAMP_MS = "timestamp_ms"
        private const val KEY_DEDUPE_KEY = "dedupe_key"

        fun notify(
            title: String,
            body: String,
            fromDeviceId: String,
            ingress: String,
            timestampMs: Long,
        ): RecentActivityEntry {
            val normalizedTitle = title.trim().ifBlank { "(untitled)" }
            val normalizedBody = body.trim()
            val normalizedFrom = fromDeviceId.trim()
            return RecentActivityEntry(
                kind = KIND_NOTIFY,
                title = normalizedTitle,
                body = normalizedBody,
                fromDeviceId = normalizedFrom,
                ingress = ingress,
                timestampMs = timestampMs,
                dedupeKey = buildDedupeKey(
                    kind = KIND_NOTIFY,
                    title = normalizedTitle,
                    body = normalizedBody,
                    fromDeviceId = normalizedFrom,
                ),
            )
        }

        fun task(
            taskKind: String,
            title: String,
            summary: String,
            fromDeviceId: String,
            ingress: String,
            timestampMs: Long,
        ): RecentActivityEntry {
            val normalizedTaskKind = taskKind.trim()
            val normalizedTitle = buildString {
                append("Codex")
                if (normalizedTaskKind.isNotBlank()) {
                    append(' ')
                    append(normalizedTaskKind)
                }
                append(": ")
                append(title.trim().ifBlank { "(untitled)" })
            }
            val normalizedBody = summary.trim()
            val normalizedFrom = fromDeviceId.trim()
            return RecentActivityEntry(
                kind = KIND_TASK,
                title = normalizedTitle,
                body = normalizedBody,
                fromDeviceId = normalizedFrom,
                ingress = ingress,
                timestampMs = timestampMs,
                dedupeKey = buildDedupeKey(
                    kind = KIND_TASK,
                    title = normalizedTitle,
                    body = normalizedBody,
                    fromDeviceId = normalizedFrom,
                ),
            )
        }

        fun fromJson(json: JSONObject): RecentActivityEntry? {
            val kind = json.optString(KEY_KIND).trim()
            val title = json.optString(KEY_TITLE).trim()
            val timestampMs = json.optLong(KEY_TIMESTAMP_MS)
            if (kind.isBlank() || title.isBlank() || timestampMs <= 0L) {
                return null
            }
            val body = json.optString(KEY_BODY).trim()
            val fromDeviceId = json.optString(KEY_FROM_DEVICE_ID).trim()
            val ingress = json.optString(KEY_INGRESS).trim().ifBlank { INGRESS_WEBSOCKET }
            val dedupeKey = json.optString(KEY_DEDUPE_KEY).trim().ifBlank {
                buildDedupeKey(
                    kind = kind,
                    title = title,
                    body = body,
                    fromDeviceId = fromDeviceId,
                )
            }
            return RecentActivityEntry(
                kind = kind,
                title = title,
                body = body,
                fromDeviceId = fromDeviceId,
                ingress = ingress,
                timestampMs = timestampMs,
                dedupeKey = dedupeKey,
            )
        }

        private fun buildDedupeKey(
            kind: String,
            title: String,
            body: String,
            fromDeviceId: String,
        ): String {
            return listOf(kind.trim(), fromDeviceId.trim(), title.trim(), body.trim())
                .joinToString("\u001F")
        }
    }
}
