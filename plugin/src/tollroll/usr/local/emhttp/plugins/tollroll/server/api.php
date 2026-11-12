<?php
/* Same-origin proxy from the WebGUI to the TollRoll service, which listens
 * on a unix socket and has no login of its own. Only a signed-in session
 * reaches this script, and Unraid checks the csrf_token of every POST before
 * it runs, so every write comes in as a POST.
 *
 *   GET  api.php?path=board&lang=<tag>       -> GET  /api/board
 *   GET  api.php?path=icons                  -> GET  /api/icons
 *   GET  api.php?path=notify                 -> GET  /api/notify
 *   GET  api.php?path=fortress               -> GET  /api/fortress
 *   GET  api.php?path=tile                   -> GET  /api/tile
 *   POST api.php  path=notify      data=JSON  -> PUT  /api/notify
 *   POST api.php  path=notify_test data=JSON  -> POST /api/notify/test
 *   POST api.php  path=join        data=JSON  -> POST /api/fortress/join
 *   POST api.php  path=leave                  -> POST /api/fortress/leave
 *   POST api.php  path=remove      data=JSON  -> POST /api/fortress/remove
 *   POST api.php  path=tile        data=JSON  -> PUT  /api/tile
 *
 * Notification secrets and the phrase go in through a POST and never come
 * back out: the service answers with channels whose tokens, passwords and
 * header values are masked, and keeps no phrase at all.
 */

$sock   = '/var/run/tollroll.sock';
$reads  = ['board' => '/api/board', 'icons' => '/api/icons', 'notify' => '/api/notify', 'fortress' => '/api/fortress',
           'tile' => '/api/tile'];
$writes = ['notify' => ['PUT', '/api/notify'], 'notify_test' => ['POST', '/api/notify/test'],
           'join' => ['POST', '/api/fortress/join'], 'leave' => ['POST', '/api/fortress/leave'],
           'remove' => ['POST', '/api/fortress/remove'],
           'tile' => ['PUT', '/api/tile']];

$fail = function ($code, $error) {
    http_response_code($code);
    header('Content-Type: application/json');
    echo json_encode(['error' => $error]);
    exit;
};

$verb = 'GET';
$body = null;
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    $path = isset($_POST['path']) ? (string) $_POST['path'] : '';
    if (!isset($writes[$path])) {
        $fail(400, 'not_allowed');
    }
    list($verb, $url) = $writes[$path];
    $body = isset($_POST['data']) ? (string) $_POST['data'] : '';
} elseif ($_SERVER['REQUEST_METHOD'] === 'GET') {
    $path = isset($_GET['path']) ? (string) $_GET['path'] : '';
    if (!isset($reads[$path])) {
        $fail(400, 'not_allowed');
    }
    $url  = $reads[$path];
    $lang = isset($_GET['lang']) ? (string) $_GET['lang'] : '';
    if ($path === 'board' && preg_match('/^[a-z]{2}$/', $lang)) {
        $url .= '?lang=' . $lang;
    }
} else {
    $fail(405, 'method_not_allowed');
}

$ch = curl_init();
curl_setopt_array($ch, [
    CURLOPT_UNIX_SOCKET_PATH => $sock,
    CURLOPT_URL              => 'http://tollroll' . $url,
    CURLOPT_CUSTOMREQUEST    => $verb,
    CURLOPT_HTTPHEADER       => ['Content-Type: application/json', 'Accept: application/json'],
    CURLOPT_RETURNTRANSFER   => true,
    CURLOPT_CONNECTTIMEOUT   => 3,
    // A channel test may take the fifteen seconds a channel gets per attempt.
    CURLOPT_TIMEOUT          => 20,
]);
if ($body !== null) {
    curl_setopt($ch, CURLOPT_POSTFIELDS, $body);
}
$resp = curl_exec($ch);
$code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
curl_close($ch);

if ($resp === false || $code === 0) {
    $fail(503, 'engine');
}
http_response_code($code);
header('Content-Type: application/json');
header('Cache-Control: no-store');
header('X-Content-Type-Options: nosniff');
echo $resp;
