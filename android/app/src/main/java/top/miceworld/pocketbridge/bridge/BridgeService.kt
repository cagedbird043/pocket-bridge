package top.miceworld.pocketbridge.bridge

import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.IBinder
import android.util.Log
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
import pocketbridge.v1.Bridge.Ack
import pocketbridge.v1.Bridge.DeviceHello
import pocketbridge.v1.Bridge.Envelope
import pocketbridge.v1.Bridge.NotifyPush
import pocketbridge.v1.Bridge.TaskStatus
import top.miceworld.pocketbridge.BridgeConfig
import top.miceworld.pocketbridge.BridgePrefs
import top.miceworld.pocketbridge.BridgeRuntime
import top.miceworld.pocketbridge.NotificationHelper
import java.util.concurrent.TimeUnit

class BridgeService : Service() {
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val httpClient = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .build()

    @Volatile
    private var running = false

    @Volatile
    private var socket: WebSocket? = null
    private var loopJob: Job? = null
    private var currentConfig: BridgeConfig? = null

    override fun onCreate() {
        super.onCreate()
        NotificationHelper.ensureChannels(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> stopBridge()
            ACTION_SEND_NOTIFY -> sendNotify(
                target = intent.getStringExtra(EXTRA_TARGET).orEmpty(),
                title = intent.getStringExtra(EXTRA_TITLE).orEmpty(),
                body = intent.getStringExtra(EXTRA_BODY).orEmpty(),
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

    private fun startBridge() {
        val config = BridgePrefs.load(this)
        currentConfig = config
        if (running) {
            BridgeRuntime.appendLog("服务已在运行")
            return
        }
        running = true
        startForeground(
            NotificationHelper.SERVICE_NOTIFICATION_ID,
            NotificationHelper.buildServiceNotification(this, connected = false),
        )
        BridgeRuntime.updateConnection(
            connected = false,
            deviceId = config.deviceId,
            relayUrl = config.relayUrl,
        )
        loopJob = serviceScope.launch {
            connectLoop(config)
        }
    }

    private fun stopBridge() {
        running = false
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
        stopSelf()
    }

    private suspend fun connectLoop(config: BridgeConfig) {
        while (running) {
            BridgeRuntime.appendLog("正在连接 ${config.relayUrl} as ${config.deviceId}")
            val request = Request.Builder()
                .url("${config.relayUrl}?device_id=${config.deviceId}")
                .header("Authorization", "Bearer ${config.token}")
                .build()

            val listener = BridgeSocketListener(config)
            val ws = httpClient.newWebSocket(request, listener)
            socket = ws
            listener.awaitClosed()

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
        val bytes = env.toByteArray()
        val ok = socket?.send(okio.ByteString.of(*bytes)) == true
        if (ok) {
            BridgeRuntime.appendLog("已发送通知到 $target: $title")
        } else {
            BridgeRuntime.appendLog("发送通知失败：当前未连接 relay")
        }
    }

    private inner class BridgeSocketListener(
        private val config: BridgeConfig,
    ) : WebSocketListener() {
        private val closed = kotlinx.coroutines.CompletableDeferred<Unit>()
        var lastError: String? = null

        override fun onOpen(webSocket: WebSocket, response: Response) {
            BridgeRuntime.appendLog("连接已建立")
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
            webSocket.send(okio.ByteString.of(*hello.toByteArray()))
        }

        override fun onMessage(webSocket: WebSocket, bytes: okio.ByteString) {
            try {
                val env = Envelope.parseFrom(bytes.toByteArray())
                when (env.payloadCase) {
                    Envelope.PayloadCase.ACK -> {
                        val ack: Ack = env.ack
                        BridgeRuntime.appendLog("收到 ACK: ${ack.ackId}")
                    }
                    Envelope.PayloadCase.NOTIFY_PUSH -> {
                        val msg = env.notifyPush
                        val body = if (msg.body.isBlank()) "(empty)" else msg.body
                        BridgeRuntime.appendLog("通知 from=${env.fromDeviceId}: ${msg.title}")
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
                        NotificationHelper.showIncomingNotification(
                            this@BridgeService,
                            title,
                            body,
                        )
                    }
                    Envelope.PayloadCase.ERROR -> {
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
            lastError = "closing code=$code reason=$reason"
            webSocket.close(code, reason)
            closed.complete(Unit)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            lastError = "closed code=$code reason=$reason"
            closed.complete(Unit)
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            lastError = t.message ?: t.javaClass.simpleName
            BridgeRuntime.appendLog("连接失败: ${lastError}")
            Log.e("PocketBridge", "websocket failure", t)
            closed.complete(Unit)
        }

        suspend fun awaitClosed() {
            closed.await()
        }
    }

    companion object {
        private const val ACTION_START = "top.miceworld.pocketbridge.action.START"
        private const val ACTION_STOP = "top.miceworld.pocketbridge.action.STOP"
        private const val ACTION_SEND_NOTIFY = "top.miceworld.pocketbridge.action.SEND_NOTIFY"
        private const val EXTRA_TARGET = "target"
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_BODY = "body"

        fun start(context: Context) {
            val intent = Intent(context, BridgeService::class.java).setAction(ACTION_START)
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

        private fun nextId(): String = System.currentTimeMillis().toString()
    }
}
