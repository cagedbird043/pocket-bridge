package top.miceworld.pocketbridge.bridge

import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import android.util.Base64
import android.util.Log
import androidx.core.app.ServiceCompat
import com.google.protobuf.ByteString
import com.google.protobuf.InvalidProtocolBufferException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.bouncycastle.crypto.params.Ed25519PrivateKeyParameters
import org.bouncycastle.crypto.signers.Ed25519Signer
import org.bouncycastle.crypto.util.PrivateKeyFactory
import pocketbridge.v1.Bridge.Ack
import pocketbridge.v1.Bridge.AuthChallenge
import pocketbridge.v1.Bridge.AuthResponse
import pocketbridge.v1.Bridge.ClipboardPull
import pocketbridge.v1.Bridge.ClipboardPush
import pocketbridge.v1.Bridge.ClipboardValue
import pocketbridge.v1.Bridge.DeviceHello
import pocketbridge.v1.Bridge.Envelope
import pocketbridge.v1.Bridge.NotifyPush
import pocketbridge.v1.Bridge.PushTokenUpdate
import pocketbridge.v1.Bridge.TaskStatus
import top.miceworld.pocketbridge.BridgeConfig
import top.miceworld.pocketbridge.BridgePrefs
import top.miceworld.pocketbridge.BridgeRuntime
import top.miceworld.pocketbridge.NotificationHelper
import top.miceworld.pocketbridge.PushBridge
import top.miceworld.pocketbridge.RecentActivityEntry
import java.util.concurrent.TimeUnit

class BridgeService : Service() {
    private val tag = "PocketBridge"
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val httpClient = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .build()

    @Volatile
    private var running = false

    @Volatile
    private var socket: WebSocket? = null

    @Volatile
    private var authenticated = false

    private var loopJob: Job? = null
    private var currentConfig: BridgeConfig? = null

    override fun onCreate() {
        super.onCreate()
        NotificationHelper.ensureChannels(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> stopBridge()
            ACTION_RESTART -> restartBridge()
            ACTION_SEND_NOTIFY -> sendNotify(
                target = intent.getStringExtra(EXTRA_TARGET).orEmpty(),
                title = intent.getStringExtra(EXTRA_TITLE).orEmpty(),
                body = intent.getStringExtra(EXTRA_BODY).orEmpty(),
            )
            ACTION_PUSH_CLIPBOARD -> pushClipboard(
                target = intent.getStringExtra(EXTRA_TARGET).orEmpty(),
            )
            ACTION_PULL_CLIPBOARD -> pullClipboard(
                target = intent.getStringExtra(EXTRA_TARGET).orEmpty(),
            )
            else -> startBridge()
        }
        return START_STICKY
    }

    override fun onDestroy() {
        stopBridge()
        serviceScope.cancel()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onTimeout(startId: Int, fgsType: Int) {
        BridgeRuntime.appendLog("前台服务超时，准备停止 (type=$fgsType)")
        Log.w(tag, "foreground service timeout, stop self: type=$fgsType startId=$startId")
        stopBridge()
    }

    private fun startBridge() {
        val config = BridgePrefs.load(this)
        currentConfig = config
        PushBridge.fetchAndStoreToken(this)
        if (running) {
            BridgeRuntime.appendLog("服务已在运行")
            return
        }
        running = true
        authenticated = false
        val started = tryStartForeground(config)
        if (!started) {
            return
        }
        BridgeRuntime.updateConnection(
            connected = false,
            deviceId = config.deviceId,
            relayUrl = config.relayUrl,
        )
        loopJob = serviceScope.launch {
            connectLoop(config)
        }
    }

    private fun restartBridge() {
        shutdownBridge(stopService = false)
        startBridge()
    }

    private fun stopBridge() {
        shutdownBridge(stopService = true)
    }

    private fun shutdownBridge(stopService: Boolean) {
        running = false
        authenticated = false
        loopJob?.cancel()
        loopJob = null
        socket?.close(1000, "stop requested")
        socket = null
        BridgeRuntime.updateConnection(
            connected = false,
            deviceId = currentConfig?.deviceId.orEmpty(),
            relayUrl = currentConfig?.relayUrl.orEmpty(),
        )
        stopForeground(STOP_FOREGROUND_REMOVE)
        if (stopService) {
            stopSelf()
        }
    }

    private fun tryStartForeground(config: BridgeConfig): Boolean {
        return try {
            ServiceCompat.startForeground(
                this,
                NotificationHelper.SERVICE_NOTIFICATION_ID,
                NotificationHelper.buildServiceNotification(this, connected = false),
                ServiceInfo.FOREGROUND_SERVICE_TYPE_REMOTE_MESSAGING,
            )
            true
        } catch (e: Exception) {
            running = false
            authenticated = false
            loopJob?.cancel()
            loopJob = null
            socket?.cancel()
            socket = null
            BridgeRuntime.appendLog("启动前台服务失败: ${e.message ?: e.javaClass.simpleName}")
            BridgeRuntime.updateConnection(
                connected = false,
                deviceId = config.deviceId,
                relayUrl = config.relayUrl,
                lastError = e.message ?: e.javaClass.simpleName,
            )
            Log.e(tag, "failed to promote BridgeService to foreground", e)
            stopSelf()
            false
        }
    }

    private suspend fun connectLoop(config: BridgeConfig) {
        while (running) {
            authenticated = false
            BridgeRuntime.appendLog("正在连接 ${config.relayUrl} as ${config.deviceId}")
            val request = Request.Builder()
                .url(config.relayUrl)
                .build()

            val listener = BridgeSocketListener(config)
            val ws = httpClient.newWebSocket(request, listener)
            socket = ws
            listener.awaitClosed()
            authenticated = false

            if (!running) {
                break
            }

            BridgeRuntime.updateConnection(
                connected = false,
                deviceId = config.deviceId,
                relayUrl = config.relayUrl,
                lastError = listener.lastError ?: "websocket disconnected",
            )
            NotificationHelper.ensureChannels(this)
            val manager = getSystemService(android.app.NotificationManager::class.java)
            manager.notify(
                NotificationHelper.SERVICE_NOTIFICATION_ID,
                NotificationHelper.buildServiceNotification(this, connected = false),
            )
            delay(2_000)
        }
    }

    private fun sendNotify(target: String, title: String, body: String) {
        val config = currentConfig ?: BridgePrefs.load(this)
        if (target.isBlank() || title.isBlank()) {
            BridgeRuntime.appendLog("发送通知失败：target/title 不能为空")
            return
        }
        val env = Envelope.newBuilder()
            .setId(nextId())
            .setFromDeviceId(config.deviceId)
            .setToDeviceId(target)
            .setUnixMs(System.currentTimeMillis())
            .setNotifyPush(
                NotifyPush.newBuilder()
                    .setTitle(title)
                    .setBody(body)
                    .setTopic("manual")
                    .setPriority("normal")
                    .build(),
            )
            .build()
        val ok = sendAuthenticatedEnvelope(env)
        if (ok) {
            BridgeRuntime.appendLog("已发送通知到 $target: $title")
        } else {
            BridgeRuntime.appendLog("发送通知失败：当前未完成 relay 认证")
        }
    }

    private fun pushClipboard(target: String) {
        val config = currentConfig ?: BridgePrefs.load(this)
        if (target.isBlank()) {
            BridgeRuntime.appendLog("发送剪贴板失败：target 不能为空")
            return
        }
        val text = readLocalClipboardText()
        if (text == null) {
            BridgeRuntime.appendLog("发送剪贴板失败：本机剪贴板不可读")
            return
        }
        val env = Envelope.newBuilder()
            .setId(nextId())
            .setFromDeviceId(config.deviceId)
            .setToDeviceId(target)
            .setUnixMs(System.currentTimeMillis())
            .setClipboardPush(
                ClipboardPush.newBuilder()
                    .setMimeType("text/plain;charset=utf-8")
                    .setText(text)
                    .build(),
            )
            .build()
        val ok = sendAuthenticatedEnvelope(env)
        if (ok) {
            BridgeRuntime.appendLog("已发送剪贴板到 $target: ${previewText(text)}")
        } else {
            BridgeRuntime.appendLog("发送剪贴板失败：当前未完成 relay 认证")
        }
    }

    private fun pullClipboard(target: String) {
        val config = currentConfig ?: BridgePrefs.load(this)
        if (target.isBlank()) {
            BridgeRuntime.appendLog("拉取剪贴板失败：target 不能为空")
            return
        }
        val env = Envelope.newBuilder()
            .setId(nextId())
            .setFromDeviceId(config.deviceId)
            .setToDeviceId(target)
            .setUnixMs(System.currentTimeMillis())
            .setClipboardPull(
                ClipboardPull.newBuilder()
                    .setPreferredMimeType("text/plain;charset=utf-8")
                    .build(),
            )
            .build()
        val ok = sendAuthenticatedEnvelope(env)
        if (ok) {
            BridgeRuntime.appendLog("已请求从 $target 拉取剪贴板")
        } else {
            BridgeRuntime.appendLog("拉取剪贴板失败：当前未完成 relay 认证")
        }
    }

    private fun sendAuthenticatedEnvelope(env: Envelope): Boolean {
        if (!authenticated) {
            return false
        }
        return socket?.send(okio.ByteString.of(*env.toByteArray())) == true
    }

    private fun syncPushToken(config: BridgeConfig) {
        val cachedToken = BridgePrefs.loadFcmToken(this)
        if (cachedToken.isNotBlank()) {
            sendPushTokenUpdate(config, cachedToken, source = "cached")
        }

        PushBridge.fetchAndStoreToken(this) { token ->
            if (token != cachedToken) {
                sendPushTokenUpdate(config, token, source = "fresh")
            }
        }
    }

    private fun sendPushTokenUpdate(
        config: BridgeConfig,
        token: String,
        source: String,
    ) {
        if (config.notifyTarget.isBlank()) {
            BridgeRuntime.appendLog("同步 FCM token 失败 ($source)：notify target 为空")
            return
        }
        val env = Envelope.newBuilder()
            .setId(nextId())
            .setFromDeviceId(config.deviceId)
            .setToDeviceId(config.notifyTarget)
            .setUnixMs(System.currentTimeMillis())
            .setPushTokenUpdate(
                PushTokenUpdate.newBuilder()
                    .setProvider(PushBridge.PROVIDER_FCM)
                    .setToken(token)
                    .setPlatform("android")
                    .setPackageName(packageName)
                    .build(),
            )
            .build()
        val ok = sendAuthenticatedEnvelope(env)
        if (ok) {
            BridgeRuntime.appendLog("已向 ${config.notifyTarget} 同步 FCM token ($source)")
        } else {
            BridgeRuntime.appendLog("同步 FCM token 失败 ($source)：当前未完成 relay 认证")
        }
    }

    private inner class BridgeSocketListener(
        private val config: BridgeConfig,
    ) : WebSocketListener() {
        private val closed = kotlinx.coroutines.CompletableDeferred<Unit>()
        var lastError: String? = null

        override fun onOpen(webSocket: WebSocket, response: Response) {
            authenticated = false
            BridgeRuntime.appendLog("连接已建立，开始设备认证")
            BridgeRuntime.updateConnection(
                connected = false,
                deviceId = config.deviceId,
                relayUrl = config.relayUrl,
            )

            val hello = Envelope.newBuilder()
                .setId(nextId())
                .setFromDeviceId(config.deviceId)
                .setUnixMs(System.currentTimeMillis())
                .setDeviceHello(
                    DeviceHello.newBuilder()
                        .setDeviceId(config.deviceId)
                        .setDeviceType("android")
                        .setProtocolVersion("v1")
                        .build(),
                )
                .build()
            if (!webSocket.send(okio.ByteString.of(*hello.toByteArray()))) {
                lastError = "send hello failed"
                BridgeRuntime.appendLog("发送 device hello 失败")
                webSocket.close(4000, "hello failed")
            }
        }

        override fun onMessage(webSocket: WebSocket, bytes: okio.ByteString) {
            try {
                val env = Envelope.parseFrom(bytes.toByteArray())
                when (env.payloadCase) {
                    Envelope.PayloadCase.AUTH_CHALLENGE -> {
                        handleAuthChallenge(webSocket, config, env.authChallenge)
                    }
                    Envelope.PayloadCase.ACK -> {
                        val ack: Ack = env.ack
                        BridgeRuntime.appendLog("收到 ACK: ${ack.ackId}")
                        if (ack.ackId == helloAckId(config.deviceId)) {
                            authenticated = true
                            BridgeRuntime.appendLog("设备认证成功")
                            BridgeRuntime.updateConnection(
                                connected = true,
                                deviceId = config.deviceId,
                                relayUrl = config.relayUrl,
                            )
                            val manager = getSystemService(android.app.NotificationManager::class.java)
                            manager.notify(
                                NotificationHelper.SERVICE_NOTIFICATION_ID,
                                NotificationHelper.buildServiceNotification(this@BridgeService, connected = true),
                            )
                            syncPushToken(config)
                        }
                    }
                    Envelope.PayloadCase.NOTIFY_PUSH -> {
                        val msg = env.notifyPush
                        val body = if (msg.body.isBlank()) "(empty)" else msg.body
                        BridgeRuntime.appendLog("通知 from=${env.fromDeviceId}: ${msg.title}")
                        BridgeRuntime.recordIncomingNotify(
                            context = this@BridgeService,
                            title = msg.title,
                            body = body,
                            fromDeviceId = env.fromDeviceId,
                            ingress = RecentActivityEntry.INGRESS_WEBSOCKET,
                            timestampMs = env.unixMs.takeIf { it > 0L } ?: System.currentTimeMillis(),
                        )
                        NotificationHelper.showIncomingNotification(
                            this@BridgeService,
                            msg.title,
                            body,
                        )
                    }
                    Envelope.PayloadCase.TASK_STATUS -> {
                        val msg: TaskStatus = env.taskStatus
                        val title = "Codex ${msg.kind}: ${msg.title}"
                        val body = msg.summary
                        BridgeRuntime.appendLog("任务状态 from=${env.fromDeviceId}: ${msg.kind} ${msg.title}")
                        BridgeRuntime.recordIncomingTask(
                            context = this@BridgeService,
                            taskKind = msg.kind,
                            title = msg.title,
                            summary = body,
                            fromDeviceId = env.fromDeviceId,
                            ingress = RecentActivityEntry.INGRESS_WEBSOCKET,
                            timestampMs = env.unixMs.takeIf { it > 0L } ?: System.currentTimeMillis(),
                        )
                        NotificationHelper.showIncomingNotification(
                            this@BridgeService,
                            title,
                            body,
                        )
                    }
                    Envelope.PayloadCase.CLIPBOARD_PUSH -> {
                        val msg = env.clipboardPush
                        writeLocalClipboardText(msg.text)
                        BridgeRuntime.appendLog("剪贴板 from=${env.fromDeviceId} 已写入本机: ${previewText(msg.text)}")
                    }
                    Envelope.PayloadCase.CLIPBOARD_PULL -> {
                        val msg = env.clipboardPull
                        val mimeType = if (msg.preferredMimeType.isBlank()) {
                            "text/plain;charset=utf-8"
                        } else {
                            msg.preferredMimeType
                        }
                        val reply = Envelope.newBuilder()
                            .setId(nextId())
                            .setFromDeviceId(config.deviceId)
                            .setToDeviceId(env.fromDeviceId)
                            .setUnixMs(System.currentTimeMillis())
                            .setClipboardValue(
                                ClipboardValue.newBuilder()
                                    .setMimeType(mimeType)
                                    .setText(readLocalClipboardText().orEmpty())
                                    .build(),
                            )
                            .build()
                        val ok = sendAuthenticatedEnvelope(reply)
                        if (ok) {
                            BridgeRuntime.appendLog("已响应 ${env.fromDeviceId} 的剪贴板拉取请求")
                        } else {
                            BridgeRuntime.appendLog("响应剪贴板拉取失败：当前未完成 relay 认证")
                        }
                    }
                    Envelope.PayloadCase.CLIPBOARD_VALUE -> {
                        val msg = env.clipboardValue
                        writeLocalClipboardText(msg.text)
                        BridgeRuntime.appendLog("剪贴板 from=${env.fromDeviceId} 已同步到本机: ${previewText(msg.text)}")
                    }
                    Envelope.PayloadCase.ERROR -> {
                        lastError = "${env.error.code} ${env.error.message}"
                        BridgeRuntime.appendLog("relay 错误: ${env.error.code} ${env.error.message}")
                    }
                    else -> {
                        BridgeRuntime.appendLog("忽略消息类型: ${env.payloadCase}")
                    }
                }
            } catch (e: InvalidProtocolBufferException) {
                BridgeRuntime.appendLog("protobuf 解码失败: ${e.message}")
            }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            authenticated = false
            lastError = "closing code=$code reason=$reason"
            webSocket.close(code, reason)
            closed.complete(Unit)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            authenticated = false
            lastError = "closed code=$code reason=$reason"
            closed.complete(Unit)
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            authenticated = false
            lastError = t.message ?: t.javaClass.simpleName
            BridgeRuntime.appendLog("连接失败: ${lastError}")
            Log.e(tag, "websocket failure", t)
            closed.complete(Unit)
        }

        suspend fun awaitClosed() {
            closed.await()
        }

        private fun handleAuthChallenge(webSocket: WebSocket, config: BridgeConfig, challenge: AuthChallenge) {
            try {
                if (challenge.algorithm != ALGORITHM_ED25519) {
                    throw IllegalArgumentException("unsupported auth algorithm: ${challenge.algorithm}")
                }
                val signature = signChallenge(config.privateKeyBase64, challenge)
                val response = Envelope.newBuilder()
                    .setId(nextId())
                    .setFromDeviceId(config.deviceId)
                    .setToDeviceId("relay")
                    .setUnixMs(System.currentTimeMillis())
                    .setAuthResponse(
                        AuthResponse.newBuilder()
                            .setSignature(ByteString.copyFrom(signature))
                            .build(),
                    )
                    .build()
                if (webSocket.send(okio.ByteString.of(*response.toByteArray()))) {
                    BridgeRuntime.appendLog("已发送设备签名响应")
                } else {
                    throw IllegalStateException("websocket send returned false")
                }
            } catch (e: Exception) {
                authenticated = false
                lastError = e.message ?: e.javaClass.simpleName
                BridgeRuntime.appendLog("认证失败: ${lastError}")
                webSocket.close(4001, "auth failed")
            }
        }
    }

    private fun readLocalClipboardText(): String? {
        val manager = getSystemService(ClipboardManager::class.java) ?: return null
        val clip = manager.primaryClip ?: return ""
        if (clip.itemCount == 0) {
            return ""
        }
        return clip.getItemAt(0).coerceToText(this)?.toString().orEmpty()
    }

    private fun writeLocalClipboardText(text: String) {
        val manager = getSystemService(ClipboardManager::class.java) ?: return
        manager.setPrimaryClip(ClipData.newPlainText("pocket-bridge", text))
    }

    companion object {
        private const val ALGORITHM_ED25519 = "ed25519"
        private const val ACTION_START = "top.miceworld.pocketbridge.action.START"
        private const val ACTION_RESTART = "top.miceworld.pocketbridge.action.RESTART"
        private const val ACTION_STOP = "top.miceworld.pocketbridge.action.STOP"
        private const val ACTION_SEND_NOTIFY = "top.miceworld.pocketbridge.action.SEND_NOTIFY"
        private const val ACTION_PUSH_CLIPBOARD = "top.miceworld.pocketbridge.action.PUSH_CLIPBOARD"
        private const val ACTION_PULL_CLIPBOARD = "top.miceworld.pocketbridge.action.PULL_CLIPBOARD"
        private const val EXTRA_TARGET = "target"
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_BODY = "body"

        fun start(context: Context) {
            val intent = Intent(context, BridgeService::class.java).setAction(ACTION_START)
            context.startForegroundService(intent)
        }

        fun restart(context: Context) {
            val intent = Intent(context, BridgeService::class.java).setAction(ACTION_RESTART)
            context.startForegroundService(intent)
        }

        fun stop(context: Context) {
            val intent = Intent(context, BridgeService::class.java).setAction(ACTION_STOP)
            context.startService(intent)
        }

        fun sendNotify(context: Context, target: String, title: String, body: String) {
            val intent = Intent(context, BridgeService::class.java)
                .setAction(ACTION_SEND_NOTIFY)
                .putExtra(EXTRA_TARGET, target)
                .putExtra(EXTRA_TITLE, title)
                .putExtra(EXTRA_BODY, body)
            context.startService(intent)
        }

        fun pushClipboard(context: Context, target: String) {
            val intent = Intent(context, BridgeService::class.java)
                .setAction(ACTION_PUSH_CLIPBOARD)
                .putExtra(EXTRA_TARGET, target)
            context.startService(intent)
        }

        fun pullClipboard(context: Context, target: String) {
            val intent = Intent(context, BridgeService::class.java)
                .setAction(ACTION_PULL_CLIPBOARD)
                .putExtra(EXTRA_TARGET, target)
            context.startService(intent)
        }

        private fun nextId(): String = System.currentTimeMillis().toString()

        private fun helloAckId(deviceId: String): String = "hello:$deviceId"

        private fun signChallenge(privateKeyBase64: String, challenge: AuthChallenge): ByteArray {
            val keyParameter = PrivateKeyFactory.createKey(Base64.decode(privateKeyBase64, Base64.DEFAULT))
            require(keyParameter is Ed25519PrivateKeyParameters) {
                "private key is not Ed25519"
            }
            val signer = Ed25519Signer()
            signer.init(true, keyParameter)
            val challengeBytes = challenge.challengeData.toByteArray()
            signer.update(challengeBytes, 0, challengeBytes.size)
            return signer.generateSignature()
        }

        private fun previewText(text: String): String {
            val normalized = text.replace("\n", "\\n")
            return if (normalized.length <= 80) normalized else normalized.take(80) + "..."
        }
    }
}
