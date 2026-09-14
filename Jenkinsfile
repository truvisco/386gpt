pipeline {
    agent any

    options {
        disableConcurrentBuilds()
        buildDiscarder(logRotator(numToKeepStr: '20'))
        timeout(time: 30, unit: 'MINUTES')
    }

    environment {
        APP_NAME = '386gpt'
        BACKEND_HOST = 'web1'
        BACKEND_ORIGIN = 'https://api-386gpt.truvis.co'
        ETCD_ENV_FILE = '/etc/etcd/jenkins.env'
        ETCD_PREFIX = '/prod/386gpt'
        SHARED_ETCD_PREFIX = '/prod/truvis.co'
    }

    stages {
        stage('Checkout') {
            steps {
                checkout scm
            }
        }

        stage('Verify') {
            parallel {
                stage('Backend') {
                    steps {
                        dir('backend') {
                            sh 'go test ./...'
                            sh 'go vet ./...'
                        }
                    }
                }
                stage('Frontend') {
                    steps {
                        dir('frontend') {
                            sh 'npm ci'
                            sh 'npm run lint'
                            sh 'npm run build'
                        }
                    }
                }
            }
        }

        stage('Build backend') {
            when { branch 'master' }
            steps {
                sh '''
                    set -eu
                    mkdir -p .deploy/release
                    cd backend
                    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../.deploy/release/386gpt .
                    cd ..
                    cp deploy/production/386gpt.service .deploy/release/
                    cp deploy/production/api-386gpt.truvis.co.nginx.conf .deploy/release/
                    cp deploy/production/install-release.sh .deploy/release/
                    chmod 0755 .deploy/release/install-release.sh
                '''
            }
        }

        stage('Prepare secrets') {
            when { branch 'master' }
            steps {
                sh '''
                    set +x
                    sh deploy/jenkins/render-production-config.sh .deploy/release/secrets
                '''
            }
        }

        stage('Deploy backend') {
            when { branch 'master' }
            steps {
                sh '''
                    set -eu
                    release_id="${BUILD_NUMBER}-$(git rev-parse --short=12 HEAD)"
                    remote_stage="/tmp/386gpt-${release_id}"
                    cleanup_remote() {
                        ssh -o BatchMode=yes "$BACKEND_HOST" "rm -rf '$remote_stage'" >/dev/null 2>&1 || true
                    }
                    trap cleanup_remote EXIT HUP INT TERM
                    ssh -o BatchMode=yes "$BACKEND_HOST" "mkdir -m 700 '$remote_stage'"
                    scp -pqr .deploy/release/. "$BACKEND_HOST:$remote_stage/"
                    ssh -o BatchMode=yes "$BACKEND_HOST" "set -e; trap 'rm -rf \"$remote_stage\"' EXIT; sudo '$remote_stage/install-release.sh' '$release_id' '$remote_stage'"
                '''
            }
        }

        stage('Configure Cloudflare') {
            when { branch 'master' }
            steps {
                sh '''
                    set +x
                    sh deploy/jenkins/with-cloudflare-env.sh node deploy/cloudflare/ensure-api-dns.mjs api-386gpt.truvis.co web1
                '''
            }
        }

        stage('Smoke test') {
            when { branch 'master' }
            steps {
                sh '''
                    set -eu
                    attempts=0
                    until curl --fail --silent --show-error --connect-timeout 10 --max-time 15 "$BACKEND_ORIGIN/health" >/dev/null; do
                        attempts=$((attempts + 1))
                        if [ "$attempts" -ge 60 ]; then
                            echo "Public health check did not become ready." >&2
                            exit 1
                        fi
                        echo "Waiting for $BACKEND_ORIGIN ($attempts/60)..."
                        sleep 5
                    done
                    curl --fail --silent --show-error "$BACKEND_ORIGIN/api/runtime" >/dev/null
                    node deploy/smoke/websocket.mjs "$BACKEND_ORIGIN"
                '''
            }
        }
    }

    post {
        always {
            sh '''
                set +x
                if [ -d .deploy/release/secrets ]; then
                    find .deploy/release/secrets -type f -exec shred -u {} + 2>/dev/null || true
                fi
                rm -rf .deploy
            '''
        }
    }
}
