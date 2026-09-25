#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <unistd.h>
#include <signal.h>
#include <libproc.h>
#include <sys/file.h>
#include <fcntl.h>

@interface AppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, WKNavigationDelegate>
@property (nonatomic, strong) NSStatusItem *statusItem;
@property (nonatomic, strong) NSWindow *window;
@property (nonatomic, strong) WKWebView *webView;
@property (nonatomic, strong) NSTextField *statusLabel;
@property (nonatomic, strong) NSTimer *healthTimer;
@property (nonatomic, assign) pid_t corePid;
@property (nonatomic, copy) NSString *coreInstanceToken;
@property (nonatomic, assign) BOOL ownsCore;
@property (nonatomic, strong) NSTask *coreTask;
@property (nonatomic, assign) uint64_t coreStartTimeSec;
@property (nonatomic, copy) NSString *dataDir;
@property (nonatomic, assign) NSInteger port;
@property (nonatomic, assign) BOOL isHealthy;
@property (nonatomic, assign) BOOL isTerminating;
@property (nonatomic, assign) BOOL alertShownForCrash;
@property (nonatomic, assign) int lockFd;
@property (nonatomic, assign) BOOL hasLock;
@property (nonatomic, assign) BOOL portConflictDetected;
@end

static dispatch_source_t g_sigterm_source = NULL;
static dispatch_source_t g_sigint_source = NULL;

@implementation AppDelegate

- (instancetype)init {
    self = [super init];
    if (self) {
        _port = 8790;
        _corePid = 0;
        _coreInstanceToken = nil;
        _ownsCore = NO;
        _coreTask = nil;
        _coreStartTimeSec = 0;
        _isHealthy = NO;
        _isTerminating = NO;
        _alertShownForCrash = NO;
        _dataDir = nil;
        _lockFd = -1;
        _hasLock = NO;
        _portConflictDetected = NO;
    }
    return self;
}

- (void)parseArguments {
    NSArray *args = [[NSProcessInfo processInfo] arguments];
    for (NSUInteger i = 0; i < [args count]; i++) {
        NSString *arg = args[i];
        if ([arg isEqualToString:@"--data-dir"] || [arg isEqualToString:@"-data-dir"]) {
            if (i + 1 < [args count]) {
                self.dataDir = args[i + 1];
                i++;
            }
        } else if ([arg hasPrefix:@"--data-dir="]) {
            self.dataDir = [arg substringFromIndex:11];
        } else if ([arg hasPrefix:@"-data-dir="]) {
            self.dataDir = [arg substringFromIndex:10];
        } else if ([arg isEqualToString:@"--port"] || [arg isEqualToString:@"-port"]) {
            if (i + 1 < [args count]) {
                self.port = [args[i + 1] integerValue];
                i++;
            }
        } else if ([arg hasPrefix:@"--port="]) {
            self.port = [[arg substringFromIndex:7] integerValue];
        }
    }
    if (!self.dataDir || self.dataDir.length == 0) {
        NSString *envDataDir = [[[NSProcessInfo processInfo] environment] objectForKey:@"RELAYHUB_DATA_DIR"];
        if (envDataDir.length > 0) {
            self.dataDir = envDataDir;
        } else {
            NSArray *paths = NSSearchPathForDirectoriesInDomains(NSApplicationSupportDirectory, NSUserDomainMask, YES);
            NSString *appSupport = [paths firstObject];
            self.dataDir = [appSupport stringByAppendingPathComponent:@"relayhub"];
        }
    }
}

- (NSString *)lockFilePath {
    return [self.dataDir stringByAppendingPathComponent:@"relayhub.lock"];
}

- (NSString *)tokenFilePath {
    return [self.dataDir stringByAppendingPathComponent:@"relayhub.token"];
}

- (BOOL)acquireFlock {
    [[NSFileManager defaultManager] createDirectoryAtPath:self.dataDir withIntermediateDirectories:YES attributes:nil error:nil];
    NSString *lockPath = [self lockFilePath];
    int fd = open([lockPath UTF8String], O_RDWR | O_CREAT, 0600);
    if (fd < 0) {
        NSLog(@"RelayHub Shell: Failed to open lock file %@: %s", lockPath, strerror(errno));
        return NO;
    }
    if (flock(fd, LOCK_EX | LOCK_NB) != 0) {
        if (errno == EWOULDBLOCK || errno == EAGAIN) {
            NSLog(@"RelayHub Shell: Another primary instance holds data directory lock (flock EWOULDBLOCK).");
        } else {
            NSLog(@"RelayHub Shell: flock failed: %s", strerror(errno));
        }
        close(fd);
        return NO;
    }
    self.lockFd = fd;
    self.hasLock = YES;
    NSLog(@"RelayHub Shell: flock lock acquired successfully on %@", lockPath);
    return YES;
}

- (void)releaseFlock {
    if (self.lockFd >= 0) {
        flock(self.lockFd, LOCK_UN);
        close(self.lockFd);
        self.lockFd = -1;
        self.hasLock = NO;
        NSLog(@"RelayHub Shell: flock lock released.");
    }
}

- (BOOL)isPortInUse:(NSInteger)port {
    int sock = socket(AF_INET, SOCK_STREAM, 0);
    if (sock < 0) return NO;
    
    struct sockaddr_in addr;
    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_port = htons((uint16_t)port);
    addr.sin_addr.s_addr = inet_addr("127.0.0.1");
    
    int result = connect(sock, (struct sockaddr *)&addr, sizeof(addr));
    close(sock);
    return (result == 0);
}

// Synchronously probe if endpoint on port is genuinely a healthy RelayHub service
- (BOOL)probeRelayHubServiceSynchronously:(NSInteger)port {
    NSURL *healthUrl = [NSURL URLWithString:[NSString stringWithFormat:@"http://127.0.0.1:%ld/api/v1/health", (long)port]];
    NSMutableURLRequest *req = [NSMutableURLRequest requestWithURL:healthUrl
                                                       cachePolicy:NSURLRequestReloadIgnoringLocalCacheData
                                                   timeoutInterval:0.8];
    [req setHTTPMethod:@"GET"];
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    __block BOOL isRelayHub = NO;
    NSURLSessionDataTask *task = [[NSURLSession sharedSession] dataTaskWithRequest:req completionHandler:^(NSData * _Nullable data, NSURLResponse * _Nullable response, NSError * _Nullable error) {
        if (!error && [response isKindOfClass:[NSHTTPURLResponse class]]) {
            NSHTTPURLResponse *httpResp = (NSHTTPURLResponse *)response;
            if (httpResp.statusCode == 200 && data.length > 0) {
                id json = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
                if ([json isKindOfClass:[NSDictionary class]] && [[json objectForKey:@"status"] isEqualToString:@"ok"]) {
                    isRelayHub = YES;
                }
            }
        }
        dispatch_semaphore_signal(sem);
    }];
    [task resume];
    dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(1.0 * NSEC_PER_SEC)));
    return isRelayHub;
}

- (uint64_t)getProcessStartTime:(pid_t)targetPid {
    if (targetPid <= 1) return 0;
    struct proc_bsdinfo procinfo;
    int ret = proc_pidinfo(targetPid, PROC_PIDTBSDINFO, 0, &procinfo, sizeof(procinfo));
    if (ret <= 0) return 0;
    return (uint64_t)procinfo.pbi_start_tvsec;
}

- (BOOL)verifyProcessIdentityAndOwnership:(pid_t)targetPid expectedStartTime:(uint64_t)expectedStart {
    if (targetPid <= 1) return NO;
    char pathbuf[PROC_PIDPATHINFO_MAXSIZE];
    memset(pathbuf, 0, sizeof(pathbuf));
    int ret = proc_pidpath(targetPid, pathbuf, sizeof(pathbuf));
    if (ret <= 0) {
        return NO;
    }
    NSString *procPath = [NSString stringWithUTF8String:pathbuf];
    NSString *fileName = procPath.lastPathComponent;
    if (!([fileName isEqualToString:@"relayhub"] || [fileName isEqualToString:@"relayhub-core"])) {
        return NO;
    }
    if (expectedStart > 0) {
        uint64_t currentStart = [self getProcessStartTime:targetPid];
        if (currentStart == 0 || currentStart != expectedStart) {
            NSLog(@"RelayHub Shell: PID %d start time mismatch (%llu vs expected %llu), PID reused!", targetPid, currentStart, expectedStart);
            return NO;
        }
    }
    return YES;
}

- (void)cleanupStaleOwnershipFiles {
    NSString *tokenFile = [self tokenFilePath];
    if (![[NSFileManager defaultManager] fileExistsAtPath:tokenFile]) {
        return;
    }
    
    NSError *err = nil;
    NSString *content = [NSString stringWithContentsOfFile:tokenFile encoding:NSUTF8StringEncoding error:&err];
    if (content && !err) {
        NSArray *lines = [content componentsSeparatedByCharactersInSet:[NSCharacterSet newlineCharacterSet]];
        pid_t existingPid = 0;
        uint64_t tokenStartTime = 0;
        for (NSString *line in lines) {
            if ([line hasPrefix:@"pid="]) {
                existingPid = (pid_t)[[line substringFromIndex:4] integerValue];
            } else if ([line hasPrefix:@"start_time="]) {
                tokenStartTime = (uint64_t)[[line substringFromIndex:11] longLongValue];
            }
        }
        if (existingPid > 0) {
            if (kill(existingPid, 0) == 0) {
                if ([self verifyProcessIdentityAndOwnership:existingPid expectedStartTime:tokenStartTime]) {
                    // Valid alive RelayHub core holds token
                    return;
                }
            }
        }
    }
    [[NSFileManager defaultManager] removeItemAtPath:tokenFile error:nil];
}

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    [self parseArguments];
    [self setupMenuBar];
    [self setupWindow];
    
    // Acquire exclusive directory flock lock
    BOOL gotLock = [self acquireFlock];
    if (gotLock) {
        [self cleanupStaleOwnershipFiles];
        // Check if port is already in use
        if ([self isPortInUse:self.port]) {
            // Port in use while we hold flock lock: fail-closed!
            NSLog(@"RelayHub Shell: Port conflict detected! Port %ld is already occupied by another service.", (long)self.port);
            self.portConflictDetected = YES;
            self.ownsCore = NO;
            self.statusLabel.stringValue = [NSString stringWithFormat:@"端口冲突: 端口 %ld 已被其他进程占用，未启动 Core 且拒绝连接", (long)self.port];
            self.statusLabel.textColor = [NSColor systemRedColor];
            [self.statusItem.button setTitle:@"⚡ RelayHub [端口冲突]"];
            [self showPortConflictAlert];
            return;
        }
        // Port free & flock acquired: start core
        [self startCore];
    } else {
        // Lock contention: another shell instance is running or holds dataDir
        NSLog(@"RelayHub Shell: Lock contention: Another primary instance holds data directory lock.");
        // Check if port is in use and probe whether it is genuinely RelayHub
        if ([self isPortInUse:self.port] && [self probeRelayHubServiceSynchronously:self.port]) {
            NSLog(@"RelayHub Shell: Verified alive RelayHub service on port %ld. Entering observer mode.", (long)self.port);
            self.ownsCore = NO;
            self.statusLabel.stringValue = @"已连接至正在运行的 Core 实例 (Observer Mode)";
            [self.statusItem.button setTitle:@"⚡ RelayHub [共享实例]"];
            [self checkHealth];
            [self reloadWebView];
        } else {
            // Foreign service or dead port
            NSLog(@"RelayHub Shell: Port conflict detected! Port %ld occupied by non-RelayHub or unknown service.", (long)self.port);
            self.portConflictDetected = YES;
            self.ownsCore = NO;
            self.statusLabel.stringValue = [NSString stringWithFormat:@"端口冲突: 端口 %ld 已被未知服务占用，拒绝接入", (long)self.port];
            self.statusLabel.textColor = [NSColor systemRedColor];
            [self.statusItem.button setTitle:@"⚡ RelayHub [端口冲突]"];
            [self showPortConflictAlert];
            return;
        }
    }
    
    // Poll healthz every second if not in conflict
    self.healthTimer = [NSTimer scheduledTimerWithTimeInterval:1.0
                                                        target:self
                                                      selector:@selector(checkHealth)
                                                      userInfo:nil
                                                       repeats:YES];
}

- (void)showPortConflictAlert {
    dispatch_async(dispatch_get_main_queue(), ^{
        char *noUi = getenv("RELAYHUB_NO_ALERT_MODAL");
        if (noUi && strcmp(noUi, "1") == 0) {
            NSLog(@"RelayHub Shell: Port conflict alert suppressed by RELAYHUB_NO_ALERT_MODAL: port %ld", (long)self.port);
            return;
        }
        NSAlert *alert = [[NSAlert alloc] init];
        alert.alertStyle = NSAlertStyleCritical;
        alert.messageText = @"端口占用冲突 (Port Conflict)";
        alert.informativeText = [NSString stringWithFormat:@"端口 %ld 已被其他进程占用，且未能确认其为受信任的 RelayHub 实例。\nRelayHub 已触发安全拦截 (Fail-Closed)，不启动本地 Core，且不加载网页。", (long)self.port];
        [alert addButtonWithTitle:@"确定 (OK)"];
        [alert runModal];
    });
}

- (void)showCoreCrashAlert:(NSString *)reason {
    dispatch_async(dispatch_get_main_queue(), ^{
        char *noUi = getenv("RELAYHUB_NO_ALERT_MODAL");
        if (noUi && strcmp(noUi, "1") == 0) {
            NSLog(@"RelayHub Shell: Crash alert suppressed by RELAYHUB_NO_ALERT_MODAL: %@", reason);
            return;
        }
        NSAlert *alert = [[NSAlert alloc] init];
        alert.alertStyle = NSAlertStyleCritical;
        alert.messageText = @"RelayHub Core 异常退出 (Watchdog Alert)";
        alert.informativeText = [NSString stringWithFormat:@"由本客户端托管的 Core 服务发生非预期退出或崩溃。\n原因: %@\n请查看运行日志或尝试从菜单重新连接。", reason];
        [alert addButtonWithTitle:@"确定 (OK)"];
        [alert runModal];
    });
}

- (NSString *)locateCoreBinary {
    NSString *bundlePath = [[NSBundle mainBundle] bundlePath];
    // Contents/Resources/relayhub-core
    NSString *inResources = [[bundlePath stringByAppendingPathComponent:@"Contents/Resources"] stringByAppendingPathComponent:@"relayhub-core"];
    if ([[NSFileManager defaultManager] isExecutableFileAtPath:inResources]) {
        return inResources;
    }
    // Contents/MacOS/relayhub-core
    NSString *inMacOS = [[bundlePath stringByAppendingPathComponent:@"Contents/MacOS"] stringByAppendingPathComponent:@"relayhub-core"];
    if ([[NSFileManager defaultManager] isExecutableFileAtPath:inMacOS]) {
        return inMacOS;
    }
    // Sibling development path
    NSString *sibling = [[[bundlePath stringByDeletingLastPathComponent] stringByDeletingLastPathComponent] stringByAppendingPathComponent:@"bin/relayhub"];
    if ([[NSFileManager defaultManager] isExecutableFileAtPath:sibling]) {
        return sibling;
    }
    return nil;
}

- (void)startCore {
    NSString *coreBinary = [self locateCoreBinary];
    if (!coreBinary) {
        NSLog(@"RelayHub Shell: Core binary relayhub-core not found!");
        self.statusLabel.stringValue = @"Core binary not found!";
        return;
    }
    
    [[NSFileManager defaultManager] createDirectoryAtPath:self.dataDir withIntermediateDirectories:YES attributes:nil error:nil];
    
    NSMutableArray *arguments = [NSMutableArray arrayWithObject:@"-full-stack"];
    if (self.dataDir && self.dataDir.length > 0) {
        [arguments addObject:@"-data-dir"];
        [arguments addObject:self.dataDir];
    }
    // Pass management-addr matching port
    NSString *mgmtAddr = [NSString stringWithFormat:@"127.0.0.1:%ld", (long)self.port];
    [arguments addObject:@"-management-addr"];
    [arguments addObject:mgmtAddr];
    
    NSTask *task = [[NSTask alloc] init];
    task.executableURL = [NSURL fileURLWithPath:coreBinary];
    task.arguments = arguments;
    self.coreTask = task;
    
    __weak AppDelegate *weakSelf = self;
    task.terminationHandler = ^(NSTask *t) {
        int status = [t terminationStatus];
        NSTaskTerminationReason reason = [t terminationReason];
        NSLog(@"RelayHub Shell: Core process terminated with status %d, reason %ld", status, (long)reason);
        
        dispatch_async(dispatch_get_main_queue(), ^{
            AppDelegate *strongSelf = weakSelf;
            if (!strongSelf) return;
            
            if (strongSelf.isTerminating) {
                return;
            }
            
            if (strongSelf.ownsCore) {
                strongSelf.ownsCore = NO;
                strongSelf.corePid = 0;
                strongSelf.coreTask = nil;
                [strongSelf removeTokenFile];
                strongSelf.isHealthy = NO;
                strongSelf.statusLabel.stringValue = [NSString stringWithFormat:@"Core 服务异常退出 (退出码: %d)", status];
                strongSelf.statusLabel.textColor = [NSColor systemRedColor];
                [strongSelf.statusItem.button setTitle:@"⚡ RelayHub [Core 崩溃]"];
                
                if (!strongSelf.alertShownForCrash) {
                    strongSelf.alertShownForCrash = YES;
                    [strongSelf showCoreCrashAlert:[NSString stringWithFormat:@"进程非预期终止，退出状态码: %d", status]];
                }
            }
        });
    };
    
    NSPipe *pipe = [NSPipe pipe];
    task.standardOutput = pipe;
    task.standardError = pipe;
    
    [[NSNotificationCenter defaultCenter] addObserverForName:NSFileHandleReadCompletionNotification
                                                      object:[pipe fileHandleForReading]
                                                       queue:nil
                                                  usingBlock:^(NSNotification * _Nonnull note) {
        NSData *data = note.userInfo[NSFileHandleNotificationDataItem];
        if (data.length > 0) {
            NSString *str = [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding];
            NSLog(@"[Core] %@", str);
            [[pipe fileHandleForReading] readInBackgroundAndNotify];
        }
    }];
    [[pipe fileHandleForReading] readInBackgroundAndNotify];
    
    NSError *error = nil;
    BOOL launched = [task launchAndReturnError:&error];
    if (!launched || error) {
        NSLog(@"RelayHub Shell: Failed to launch Core: %@", error.localizedDescription);
        self.statusLabel.stringValue = [NSString stringWithFormat:@"启动 Core 失败: %@", error.localizedDescription];
        self.coreTask = nil;
        return;
    }
    
    self.corePid = task.processIdentifier;
    self.coreStartTimeSec = [self getProcessStartTime:self.corePid];
    self.coreInstanceToken = [[NSUUID UUID] UUIDString];
    
    // Immediate identity and ownership verification
    if (![self verifyProcessIdentityAndOwnership:self.corePid expectedStartTime:self.coreStartTimeSec]) {
        NSLog(@"RelayHub Shell: Security Warning! Spawned PID %d failed verification! Terminating.", self.corePid);
        [task terminate];
        self.corePid = 0;
        self.coreTask = nil;
        return;
    }
    
    self.ownsCore = YES;
    [self recordOwnershipFile];
    NSLog(@"RelayHub Shell: Core process launched with PID %d, token: %@, ownership acquired.", self.corePid, self.coreInstanceToken);
}

- (void)recordOwnershipFile {
    NSString *tokenFile = [self tokenFilePath];
    NSString *record = [NSString stringWithFormat:@"pid=%d\ntoken=%@\nshell_pid=%d\nstart_time=%llu\ntimestamp=%f\n",
                        self.corePid, self.coreInstanceToken, getpid(), self.coreStartTimeSec, [[NSDate date] timeIntervalSince1970]];
    [record writeToFile:tokenFile atomically:YES encoding:NSUTF8StringEncoding error:nil];
}

- (void)removeTokenFile {
    NSString *tokenFile = [self tokenFilePath];
    if ([[NSFileManager defaultManager] fileExistsAtPath:tokenFile]) {
        NSError *err = nil;
        NSString *content = [NSString stringWithContentsOfFile:tokenFile encoding:NSUTF8StringEncoding error:&err];
        if (content && [content containsString:[NSString stringWithFormat:@"token=%@", self.coreInstanceToken]]) {
            [[NSFileManager defaultManager] removeItemAtPath:tokenFile error:nil];
        }
    }
}

- (void)stopCore {
    if (!self.ownsCore || self.corePid <= 0) {
        NSLog(@"RelayHub Shell: Not owner of Core or invalid PID, skipping stopCore.");
        [self releaseFlock];
        return;
    }
    
    pid_t targetPid = self.corePid;
    NSTask *task = self.coreTask;
    
    // Strict identity and start_time verification
    if (![self verifyProcessIdentityAndOwnership:targetPid expectedStartTime:self.coreStartTimeSec]) {
        NSLog(@"RelayHub Shell: Aborting kill! PID %d does not match expected Core identity or start time.", targetPid);
        self.corePid = 0;
        self.ownsCore = NO;
        self.coreTask = nil;
        [self removeTokenFile];
        [self releaseFlock];
        return;
    }
    
    NSLog(@"RelayHub Shell: Stopping owned Core process (PID %d)...", targetPid);
    if (task && [task isRunning]) {
        [task terminate];
    } else {
        kill(targetPid, SIGTERM);
    }
    
    // Wait up to 3 seconds for graceful exit
    for (int i = 0; i < 30; i++) {
        if (kill(targetPid, 0) != 0) {
            break;
        }
        usleep(100000);
    }
    
    // Force kill only if verified alive and still same process
    if (kill(targetPid, 0) == 0) {
        if ([self verifyProcessIdentityAndOwnership:targetPid expectedStartTime:self.coreStartTimeSec]) {
            NSLog(@"RelayHub Shell: Core PID %d still alive, sending SIGKILL", targetPid);
            kill(targetPid, SIGKILL);
        }
    }
    
    self.corePid = 0;
    self.ownsCore = NO;
    self.coreTask = nil;
    [self removeTokenFile];
    [self releaseFlock];
}

- (void)checkHealth {
    if (self.portConflictDetected) {
        return;
    }
    NSURL *healthUrl = [NSURL URLWithString:[NSString stringWithFormat:@"http://127.0.0.1:%ld/healthz", (long)self.port]];
    NSMutableURLRequest *req = [NSMutableURLRequest requestWithURL:healthUrl
                                                       cachePolicy:NSURLRequestReloadIgnoringLocalCacheData
                                                   timeoutInterval:1.5];
    
    [[[NSURLSession sharedSession] dataTaskWithRequest:req completionHandler:^(NSData * _Nullable data, NSURLResponse * _Nullable response, NSError * _Nullable error) {
        BOOL ok = NO;
        if (!error && [response isKindOfClass:[NSHTTPURLResponse class]]) {
            NSHTTPURLResponse *httpResp = (NSHTTPURLResponse *)response;
            if (httpResp.statusCode == 200) {
                ok = YES;
            }
        }
        
        dispatch_async(dispatch_get_main_queue(), ^{
            if (self.portConflictDetected) return;
            if (ok && !self.isHealthy) {
                self.isHealthy = YES;
                self.statusLabel.stringValue = self.ownsCore ? @"Core 服务正常运行 (Healthy)" : @"Core 服务正常运行 (共享/非持有实例)";
                self.statusLabel.textColor = [NSColor systemGreenColor];
                [self.statusItem.button setTitle:@"⚡ RelayHub [运行中]"];
                [self reloadWebView];
            } else if (!ok && self.isHealthy) {
                self.isHealthy = NO;
                self.statusLabel.stringValue = @"Core 服务异常或离线 (Unhealthy)";
                self.statusLabel.textColor = [NSColor systemRedColor];
                [self.statusItem.button setTitle:@"⚡ RelayHub [未连接]"];
            }
        });
    }] resume];
}

- (void)setupMenuBar {
    self.statusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
    [self.statusItem.button setTitle:@"⚡ RelayHub"];
    
    NSMenu *menu = [[NSMenu alloc] init];
    [menu addItemWithTitle:@"显示控制面板 (Show Dashboard)" action:@selector(showWindow) keyEquivalent:@"d"];
    [menu addItemWithTitle:@"在浏览器中打开 (Open in Browser)" action:@selector(openExternalBrowser) keyEquivalent:@"b"];
    [menu addItem:[NSMenuItem separatorItem]];
    [menu addItemWithTitle:@"刷新界面 (Reload)" action:@selector(reloadWebView) keyEquivalent:@"r"];
    [menu addItem:[NSMenuItem separatorItem]];
    [menu addItemWithTitle:@"退出 RelayHub" action:@selector(quitApp) keyEquivalent:@"q"];
    
    self.statusItem.menu = menu;
}

- (void)setupWindow {
    NSRect screenRect = [[NSScreen mainScreen] visibleFrame];
    CGFloat width = 1100;
    CGFloat height = 750;
    CGFloat x = screenRect.origin.x + (screenRect.size.width - width) / 2.0;
    CGFloat y = screenRect.origin.y + (screenRect.size.height - height) / 2.0;
    NSRect frame = NSMakeRect(x, y, width, height);

    NSWindowStyleMask mask = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
    self.window = [[NSWindow alloc] initWithContentRect:frame styleMask:mask backing:NSBackingStoreBuffered defer:NO];
    [self.window setTitle:@"RelayHub - 统一出口与自动签到聚合器"];
    [self.window center];
    [self.window setReleasedWhenClosed:NO];
    [self.window setDelegate:self];

    // Status bar container at the bottom
    NSRect statusRect = NSMakeRect(0, 0, width, 26);
    NSView *statusView = [[NSView alloc] initWithFrame:statusRect];
    [statusView setAutoresizingMask:(NSViewWidthSizable | NSViewMaxYMargin)];
    
    self.statusLabel = [[NSTextField alloc] initWithFrame:NSMakeRect(12, 4, width - 24, 18)];
    [self.statusLabel setEditable:NO];
    [self.statusLabel setBordered:NO];
    [self.statusLabel setBackgroundColor:[NSColor clearColor]];
    [self.statusLabel setStringValue:@"正在连接 Core 服务..."];
    [self.statusLabel setFont:[NSFont systemFontOfSize:12]];
    [self.statusLabel setAutoresizingMask:(NSViewWidthSizable)];
    [statusView addSubview:self.statusLabel];
    [self.window.contentView addSubview:statusView];

    // WebKit View
    NSRect webFrame = NSMakeRect(0, 26, width, height - 26);
    WKWebViewConfiguration *config = [[WKWebViewConfiguration alloc] init];
    self.webView = [[WKWebView alloc] initWithFrame:webFrame configuration:config];
    [self.webView setAutoresizingMask:(NSViewWidthSizable | NSViewHeightSizable)];
    [self.webView setNavigationDelegate:self];
    [self.window.contentView addSubview:self.webView];

    // Note: Do not blindly reloadWebView here before port conflict verification!
    // reloadWebView will be called once healthz is confirmed or observer mode is verified.
    // orderFrontRegardless shows the window even though the app is not yet the
    // active app at this early launch point; a plain orderFront is "conditional"
    // and leaves the window hidden until activation (observed: window created but
    // never visible, only the menu bar showed). Re-assert on the next runloop tick
    // so the presentation survives the synchronous flock/port/startCore sequence
    // that follows in applicationDidFinishLaunching.
    [self.window makeKeyAndOrderFront:nil];
    [self.window orderFrontRegardless];
    [NSApp activateIgnoringOtherApps:YES];
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp activateIgnoringOtherApps:YES];
        [self.window makeKeyAndOrderFront:nil];
        [self.window orderFrontRegardless];
    });
}

// The core authenticates its management API with the token it generates into
// <data-dir>/management.token (AUDIT 2026-09-24 F4). The shell owns that data
// directory, so it reads the token and hands it to its own web view; nil when
// the file is missing (management_auth "off" or an explicit token).
- (NSString *)managementToken {
    if (self.dataDir.length == 0) return nil;
    NSString *path = [self.dataDir stringByAppendingPathComponent:@"management.token"];
    NSString *raw = [NSString stringWithContentsOfFile:path encoding:NSUTF8StringEncoding error:nil];
    NSString *token = [raw stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
    return token.length > 0 ? token : nil;
}

- (NSString *)consoleOrigin {
    return [NSString stringWithFormat:@"http://127.0.0.1:%ld", (long)self.port];
}

// Seeds the console's localStorage with the management token before any page
// script runs. The script only fires on the console's own origin, so a page
// navigated to elsewhere never sees the token. WKWebView offers no
// window.prompt, so without this the embedded console could not log in.
- (void)installManagementTokenScript {
    WKUserContentController *ucc = self.webView.configuration.userContentController;
    [ucc removeAllUserScripts];
    NSString *token = [self managementToken];
    if (token == nil) return;
    NSData *json = [NSJSONSerialization dataWithJSONObject:@[[self consoleOrigin], token] options:0 error:nil];
    if (json == nil) return;
    NSString *args = [[NSString alloc] initWithData:json encoding:NSUTF8StringEncoding];
    NSString *source = [NSString stringWithFormat:
        @"(function(v){try{if(location.origin===v[0]){localStorage.setItem('relayhub.managementToken',v[1]);}}catch(e){}})(%@);", args];
    WKUserScript *script = [[WKUserScript alloc] initWithSource:source
                                                  injectionTime:WKUserScriptInjectionTimeAtDocumentStart
                                               forMainFrameOnly:YES];
    [ucc addUserScript:script];
}

- (void)reloadWebView {
    if (self.portConflictDetected) {
        return;
    }
    [self installManagementTokenScript];
    NSURL *url = [NSURL URLWithString:[self consoleOrigin]];
    [self.webView loadRequest:[NSURLRequest requestWithURL:url]];
}

- (void)showWindow {
    [NSApp activateIgnoringOtherApps:YES];
    [self.window makeKeyAndOrderFront:nil];
}

// Opens the console in the default browser with a one-time pairing link, so
// the browser gets its own session without the token ever appearing in a URL
// (AUDIT 2026-09-24 F4). Falls back to the plain console address, where the
// console asks for a pairing code or the token.
- (void)openExternalBrowser {
    if (self.portConflictDetected) return;
    NSString *base = [self consoleOrigin];
    NSString *token = [self managementToken];
    if (token == nil) {
        [[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:base]];
        return;
    }
    NSMutableURLRequest *req = [NSMutableURLRequest requestWithURL:[NSURL URLWithString:[base stringByAppendingString:@"/api/v1/auth/pair-codes"]]];
    req.HTTPMethod = @"POST";
    req.timeoutInterval = 5;
    [req setValue:[@"Bearer " stringByAppendingString:token] forHTTPHeaderField:@"Authorization"];
    [[[NSURLSession sharedSession] dataTaskWithRequest:req completionHandler:^(NSData * _Nullable data, NSURLResponse * _Nullable response, NSError * _Nullable error) {
        NSString *target = base;
        NSInteger status = [response isKindOfClass:[NSHTTPURLResponse class]] ? ((NSHTTPURLResponse *)response).statusCode : 0;
        if (error == nil && status == 201 && data != nil) {
            id obj = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
            id link = [obj isKindOfClass:[NSDictionary class]] ? ((NSDictionary *)obj)[@"url"] : nil;
            if ([link isKindOfClass:[NSString class]] && [(NSString *)link hasPrefix:[base stringByAppendingString:@"/#pair="]]) {
                target = link;
            }
        }
        dispatch_async(dispatch_get_main_queue(), ^{
            [[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:target]];
        });
    }] resume];
}

- (void)quitApp {
    [NSApp terminate:nil];
}

- (BOOL)windowShouldClose:(NSWindow *)sender {
    [sender orderOut:nil];
    return NO;
}

// Clicking the Dock icon (or reopening) when no window is visible must re-show
// the dashboard window instead of doing nothing.
- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)flag {
    if (!flag && self.window && !self.portConflictDetected) {
        [self.window makeKeyAndOrderFront:nil];
        [self.window orderFrontRegardless];
        [NSApp activateIgnoringOtherApps:YES];
    }
    return YES;
}

- (void)applicationWillTerminate:(NSNotification *)notification {
    NSLog(@"RelayHub Shell: Application terminating, executing clean teardown...");
    self.isTerminating = YES;
    [self.healthTimer invalidate];
    [self stopCore];
}

@end

static void setupSignalHandlers(void) {
    // Ignore direct signal so dispatch source handles it safely
    signal(SIGTERM, SIG_IGN);
    signal(SIGINT, SIG_IGN);
    
    g_sigterm_source = dispatch_source_create(DISPATCH_SOURCE_TYPE_SIGNAL, SIGTERM, 0, dispatch_get_main_queue());
    if (g_sigterm_source) {
        dispatch_source_set_event_handler(g_sigterm_source, ^{
            NSLog(@"RelayHub Shell: Caught SIGTERM via dispatch signal source, terminating NSApp cleanly");
            [NSApp terminate:nil];
        });
        dispatch_resume(g_sigterm_source);
    }
    
    g_sigint_source = dispatch_source_create(DISPATCH_SOURCE_TYPE_SIGNAL, SIGINT, 0, dispatch_get_main_queue());
    if (g_sigint_source) {
        dispatch_source_set_event_handler(g_sigint_source, ^{
            NSLog(@"RelayHub Shell: Caught SIGINT via dispatch signal source, terminating NSApp cleanly");
            [NSApp terminate:nil];
        });
        dispatch_resume(g_sigint_source);
    }
}

int main(int argc, const char * argv[]) {
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "-version") == 0 || strcmp(argv[i], "--version") == 0) {
            NSString *bundlePath = [[NSBundle mainBundle] bundlePath];
            NSString *inResources = [[bundlePath stringByAppendingPathComponent:@"Contents/Resources"] stringByAppendingPathComponent:@"relayhub-core"];
            if ([[NSFileManager defaultManager] isExecutableFileAtPath:inResources]) {
                NSTask *t = [[NSTask alloc] init];
                t.executableURL = [NSURL fileURLWithPath:inResources];
                t.arguments = @[@"-version"];
                [t launchAndReturnError:nil];
                [t waitUntilExit];
                return [t terminationStatus];
            }
            printf("RelayHub Desktop 1.0.0 (Core standalone)\n");
            return 0;
        }
    }
    
    setupSignalHandlers();
    
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];
        
        AppDelegate *delegate = [[AppDelegate alloc] init];
        [app setDelegate:delegate];
        
        [app run];
    }
    return 0;
}
