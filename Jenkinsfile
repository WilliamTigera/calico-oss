pipeline{
    agent { label 'slave' }
    triggers{
        pollSCM('H/5 * * * *')
        cron('H H(0-7) * * 1-5')
    }
    environment {
        CALICOQ_IMAGE_NAME = "gcr.io/unique-caldron-775/cnx/tigera/calicoq"
        QUERYSERVER_IMAGE_NAME = "gcr.io/unique-caldron-775/cnx/tigera/cnx-queryserver"
        WAVETANK_SERVICE_ACCT = "wavetank@unique-caldron-775.iam.gserviceaccount.com"
    }
    stages {
        stage('Checkout') {
            steps {
                checkout scm
                    script {
                        currentBuild.description = """
                        BRANCH_NAME=${env.BRANCH_NAME}
                        JOB_NAME=${env.JOB_NAME}
                        CALICOQ_IMAGE_NAME=${env.CALICOQ_IMAGE_NAME}:${env.BRANCH_NAME}
                        QUERYSERVER_IMAGE_NAME=${env.QUERYSERVER_IMAGE_NAME}:${env.BRANCH_NAME}
                        BUILD_INFO=${env.RUN_DISPLAY_URL}""".stripIndent()
                    }
            }
        }
        stage('Clean artifacts') {
            steps {
                sh 'if [ -d vendor ] ; then sudo chown -R $USER:$USER vendor; fi && make clean'
            }
        }

        stage('Check cnx-queryserver changes') {
            steps {
                script {
                    SKIP_CNX_QUERYSERVER_BUILD = sh(returnStatus: true, script: "git diff --name-only HEAD^ | grep '^web/'")
                }
            }
        }

        stage('Build cnx-queryserver') {
            when {
                expression { SKIP_CNX_QUERYSERVER_BUILD == 0 }
            }
            // only run if there were cnx-queryserver changes returned from running git diff grep queries
            steps {
                dir('web') {
                    withCredentials([sshUserPrivateKey(credentialsId: 'marvin-tigera-ssh-key', keyFileVariable: 'SSH_KEY', passphraseVariable: '', usernameVariable: '')]) {
                        sh 'if [ -z "$SSH_AUTH_SOCK" ] ; then eval `ssh-agent -s`; ssh-add $SSH_KEY || true; fi && make tigera/cnx-queryserver'
                    }
                }
            }
        }

        stage('Run cnx-queryserver UTs') {
            when {
                expression { SKIP_CNX_QUERYSERVER_BUILD == 0 }
            }
            steps {
                dir('web') {
                    sh 'make ut-containerized'
                }
            }
        }

        stage('Run cnx-queryserver FVs') {
            when {
                expression { SKIP_CNX_QUERYSERVER_BUILD == 0 }
            }
            steps {
                dir('web') {
                    sh 'make fv-containerized'
                }
            }
        }

        stage('Run cnx-queryserver STs') {
            when {
                expression { SKIP_CNX_QUERYSERVER_BUILD == 0 }
            }
            steps {
                dir('web') {
                    sh 'make st-containerized'
                }
            }
        }
        stage('Push cnx-queryserver image to GCR') {
            when {
                expression { SKIP_CNX_QUERYSERVER_BUILD == 0 }
            }
            // only run if there were cnx-queryserver changes returned from running git diff grep queries
            steps {
                dir('web') {
                    script {
                        withCredentials([file(credentialsId: 'wavetank_service_account', variable: 'DOCKER_AUTH')]) {
                            if (env.BRANCH_NAME == 'master') {
                                sh "cp $DOCKER_AUTH key.json"
                                sh "gcloud auth activate-service-account ${env.WAVETANK_SERVICE_ACCT} --key-file key.json"
                                sh "gcloud docker --authorize-only --server gcr.io"

                                sh "docker tag tigera/cnx-queryserver:latest ${env.QUERYSERVER_IMAGE_NAME}:${env.BRANCH_NAME}"
                                sh "docker push ${env.QUERYSERVER_IMAGE_NAME}:${env.BRANCH_NAME}"

                                // Clean up images.
                                // Hackey since empty displayed tags are not empty according to gcloud filter criteria
                                sh """
                                    for digest in \$(gcloud container images list-tags ${env.QUERYSERVER_IMAGE_NAME} --format='get(digest)'); do
                                    if ! test \$(echo \$(gcloud container images list-tags ${env.QUERYSERVER_IMAGE_NAME} --filter=digest~\${digest}) | awk '{print \$6}'); then
                                        gcloud container images delete -q --force-delete-tags "${env.QUERYSERVER_IMAGE_NAME}@\${digest}"
                                    fi
                                    done
                                """
                            }
                        }
                    }
                }
            }
        }

        stage('Check calicoq changes') {
            steps {
                script {
                    SKIP_CALICOQ_BUILD = sh(returnStatus: true, script: "git diff --name-only HEAD^ | grep -v '^web/'")
                }
            }
        }
        stage('Build calicoq') {
            when {
                expression { SKIP_CALICOQ_BUILD == 0 }
            }
             steps {
                 withCredentials([sshUserPrivateKey(credentialsId: 'marvin-tigera-ssh-key', keyFileVariable: 'SSH_KEY', passphraseVariable: '', usernameVariable: '')]) {
                     sh 'if [ -z "$SSH_AUTH_SOCK" ] ; then eval `ssh-agent -s`; ssh-add $SSH_KEY || true; fi && make bin/calicoq'
                 }
             }
        }

        stage('Run calicoq UTs') {
            when {
                expression { SKIP_CALICOQ_BUILD == 0 }
            }
            steps {
                sh 'make ut-containerized'
            }
        }

        stage('Run calicoq FVs') {
            when {
                expression { SKIP_CALICOQ_BUILD == 0 }
            }
            steps {
                sh 'make fv-containerized'
            }
        }

        stage('Run calicoq STs') {
            when {
                expression { SKIP_CALICOQ_BUILD == 0 }
            }
            steps {
                sh 'make st-containerized'
            }
        }

        stage('Push calicoq image to GCR') {
            when {
                expression { SKIP_CALICOQ_BUILD == 0 }
            }
            steps {
                script {
                    withCredentials([file(credentialsId: 'wavetank_service_account', variable: 'DOCKER_AUTH')]) {
                        if (env.BRANCH_NAME == 'master') {
                            sh "cp $DOCKER_AUTH key.json"
                            sh "gcloud auth activate-service-account ${env.WAVETANK_SERVICE_ACCT} --key-file key.json"
                            sh "gcloud docker --authorize-only --server gcr.io"

                            sh 'make build-image'
                            sh "docker tag tigera/calicoq:latest ${env.CALICOQ_IMAGE_NAME}:${env.BRANCH_NAME}"
                            sh "docker push ${env.CALICOQ_IMAGE_NAME}:${env.BRANCH_NAME}"

                            // Clean up images.
                            // Hackey since empty displayed tags are not empty according to gcloud filter criteria
                            sh """
                                for digest in \$(gcloud container images list-tags ${env.CALICOQ_IMAGE_NAME} --format='get(digest)'); do
                                if ! test \$(echo \$(gcloud container images list-tags ${env.CALICOQ_IMAGE_NAME} --filter=digest~\${digest}) | awk '{print \$6}'); then
                                    gcloud container images delete -q --force-delete-tags "${env.CALICOQ_IMAGE_NAME}@\${digest}"
                                fi
                                done
                            """
                        }
                    }
                }
            }
        }
    }
    post {
        success {
            echo "Yay, we passed."
        }
        failure {
            echo "Boo, we failed."
            script {
                if (env.BRANCH_NAME == 'master') {
                    slackSend message: "Failure during ${env.JOB_NAME} CI!\n${env.RUN_DISPLAY_URL}", color: "warning", channel: "cnx-ci-failures"
                }
            }
        }
    }
}
