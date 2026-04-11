package top.miceworld.pocketbridge

import android.app.Activity
import android.app.NotificationManager
import android.content.ClipData
import android.content.ClipboardManager
import android.os.Bundle
import android.widget.Toast

class NotificationCopyActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        if (intent?.action != NotificationHelper.ACTION_COPY_NOTIFICATION) {
            finishSilently()
            return
        }

        val title = intent.getStringExtra(NotificationHelper.EXTRA_COPY_TITLE).orEmpty()
        val body = intent.getStringExtra(NotificationHelper.EXTRA_COPY_BODY).orEmpty()
        val text = NotificationHelper.buildClipboardText(title, body)
        if (text.isBlank()) {
            BridgeRuntime.appendLog("复制通知失败：通知内容为空")
            finishSilently()
            return
        }

        val clipboardManager = getSystemService(ClipboardManager::class.java)
        if (clipboardManager == null) {
            BridgeRuntime.appendLog("复制通知失败：剪贴板服务不可用")
            finishSilently()
            return
        }

        clipboardManager.setPrimaryClip(ClipData.newPlainText("pocket-bridge-notification", text))
        intent.getIntExtra(NotificationHelper.EXTRA_NOTIFICATION_ID, -1)
            .takeIf { it >= 0 }
            ?.let { notificationId ->
                getSystemService(NotificationManager::class.java)?.cancel(notificationId)
            }
        BridgeRuntime.appendLog("已复制通知到剪贴板: ${previewText(text)}")
        Toast.makeText(this, R.string.copy_notification_toast, Toast.LENGTH_SHORT).show()
        finishSilently()
    }

    private fun finishSilently() {
        finish()
    }

    private fun previewText(text: String): String {
        val normalized = text.replace("\n", "\\n")
        return if (normalized.length <= 80) normalized else normalized.take(80) + "..."
    }
}
