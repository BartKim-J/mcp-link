# Go 베이스 이미지
FROM golang:1.23-alpine AS build

# 작업 디렉토리 설정
WORKDIR /app

# 종속성 복사 및 설치
COPY go.mod go.sum ./
RUN go mod download

# 소스 복사 및 빌드
COPY . .
RUN go build -tags netgo -ldflags '-s -w' -o mcp-link

# 실제 실행용 이미지
FROM alpine:latest

WORKDIR /root/

# 빌드 결과 복사
COPY --from=build /app/mcp-link .

# HTTP 포트
ENV PORT=8080
EXPOSE 8080

# 서버 실행 (PORT 환경변수 사용 가능)
CMD ["./mcp-link", "serve", "--host", "0.0.0.0", "--port", "8080"]
