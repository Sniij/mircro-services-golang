## Micro Services Architecture Project
#### MSA 연습을 위한 프로젝트

##### 주요 스펙
1. 뉴스 크롤링
- 뉴스 출처: 네이버 뉴스
- HTML 파싱: goquery 라이브러리를 활용해 HTML 문서 파싱.

2. 요약 및 변환 (GPT 활용)
- openai-go 라이브러리를 사용해 GPT API와 통신.
- GPT API 를 통한 광고 태그 및 형식에 맞지 않는 태그 제거 요청.
- GPT API 를 통한 뉴스 요약
- 프롬프트를 이용한 단계적 정밀 가공

3. Markdown 형식 변환
- GPT 에게 받은 응답을 정해놓은 markdown 규격으로 변환

4. S3 업로드
- 변환된 markdown bytes 를 받아서 인코딩 후 markdown 파일로 S3 에 업로드
- 날짜별 디렉토리에 저장(news/yyyy-MM-DD/yyyy-MM-DD_{category}_{count}.md)

4. GitHub 푸시
- GitHub API 연동: 
    - oauth2 패키지를 통해 github token 으로 auth 인증
    - go-git 패키지를 활용해 푸시 구현.
- 해당 날짜에 S3 에 업로드 된 파일 목록을 읽어 정해놓은 GitHub Repository 에 업로드

5. 스케줄러
- AWS Lambda 에 배포하여 EventBridge 와 연동하여 cron 표현식을 통해 매일 9시 실행.

6. CI/CD
- 각 기능별 branch에 push/pr 시 GitHub Actions 를 통한 독립적인 배포